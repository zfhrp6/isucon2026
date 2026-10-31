# usersテーブル/行ロックの競合範囲を調べる

## 管理状況

- 状態: **登録側の対策を実装、期間開始側とスコア効果は未検証**
- 対応範囲・完了条件: 登録・投稿期間開始時のテーブルロックと、API transaction内のusers行ロックの待ち・阻害範囲を切り分ける。登録側の対策を実装し、期間開始側・行ロックの調査は継続する。
- 実装記録: 登録側の全体テーブルロックをusername一意制約へ置換する変更と既存DB向けmigrationを追加。未配備。
- 検証済み/未検証: PostgreSQLログ7スナップショットで1秒以上の実待ちとholder PID一覧を確認。最新スナップショットでは負荷区間中の採点対象プロフィール更新待ちを確認し、期間開始候補の待ちも観測。登録変更のDB統合、スキーマ初期化・reset・再起動後の永続性、旧登録処理に対するテスト感度は隔離したPostgreSQL 16と架空fixtureで確認済み。実DB配備・実ユーザーデータ・スコア比較は未実施。
- 期待効果: **中（仮説）**。実測効果: **採点対象更新の待ちは観測、実装後の得点効果は未計測**。
- 関連issue: [14 投稿期間ロック競合](14-posting-window-lock-investigation.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

調査時点では[`registerHandler`](../webapp/go/user_api.go#L93) と[`openPostingWindowHandler`](../webapp/go/admin_posting_window_api.go#L25) の両方が `users` に `SHARE ROW EXCLUSIVE` ロックを取得していた。登録側はusername一意制約へ置換済みで、現行コードでこのテーブルロックを維持しているのは期間開始側だけ。issue 14は `posting_windows` 行ロックが対象で、こちらとは別のロック機構。

## users行ロックと追加ログの観測（別時間窓）

加えて複数のAPI transactionがusers行を`FOR NO KEY UPDATE`する。関係変更ヘルパー[`beginRelationshipTx`](../webapp/go/relationship.go#L30)は主体と対象の行を順にロックする。コメント作成も両ユーザーをロックして認可SQLを実行するが、この経路では画像を書き込まない（[`comment_api.go`](../webapp/go/comment_api.go#L31)）。RealMoji作成は[`beginRelationshipTxWithActor`](../webapp/go/relationship.go#L51)を通じて両ユーザーをロックし、認可SQLと画像書き込みを行ってからcommitする（[`realmoji_api.go`](../webapp/go/realmoji_api.go#L35)）。ログインも秘密鍵を検証する前にusername該当行をロックする（[`user_api.go`](../webapp/go/user_api.go#L136)）。登録/期間開始のテーブルロックと、これらの行ロックは別経路なので、競合関係を分けて調べる。issue 14は`posting_windows`行ロックが対象。

投稿作成は先にusers行をロックし、その後`posting_windows`行を`FOR UPDATE`する（[`post_api.go`](../webapp/go/post_api.go#L96)、[`post_api.go`](../webapp/go/post_api.go#L107)）。そのため投稿期間ロックを待つtransactionがusers行ロックを保持し、同じ利用者の別操作へ待ちを波及させる連鎖が考えられる。これはコードから分かるロック順序に基づく仮説で、保存ログから個別blockerやこの連鎖が実際に起きたとは確認できていない。

[05:52:00,05:55:00) UTCに限定した `logs/postgresql/20261031T055547Z-WOSEwV/isu-asia2/postgresql.log` のログ突合では、`log_lock_waits` によるusers行ロック取得までの待機が61件、テーブルロックが21件記録されている。いずれも `deadlock_timeout=1s` を超えた待機で、記録された待機時間の最大はそれぞれ3.880秒、6.024秒。100msはduration集計の閾値であり、この待機件数には適用していない。スナップショットは複数時間帯・走行を含み、初期検証や実負荷との帰属、blocking API、稼働artifact、採点への影響は未確認。


## 調査・制約

- 遅いSQL記録を特定の競合と同一視せず、ロック取得待ち・保持時間、blocking関係、同時登録・期間開始・ユーザー行をロックするAPIの成功率を同時刻で観測する。ログ上の `still waiting` と `acquired` は同一待機の対として数え、累積ログ内の別走行を一つの負荷結果にまとめない。
- 期間開始側の `users` テーブルロックは維持し、登録側だけを対象にする。username一意制約を本番DBへ適用する前に既存重複とindex状態を確認し、重複を手作業で解決する。

## 既存ログの調査結果（2026-10-31）

本体リポジトリの `logs/postgresql/*/isu-asia2/postgresql.log` にある7つの独立したスナップショットを確認した。PostgreSQL 16の設定記録では `log_lock_waits=on`、`deadlock_timeout=1s`、`log_min_duration_statement=100ms`、パラメーター記録長0。待ちログのrelation OIDは全スナップショットで16684で、同じログに `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` の実行が記録されている。

| UTCスナップショット | 1秒以上待った取得イベント | ロック文の記録数（100ms以上） | ロック文の記録時間合計 / 最大 |
|---|---:|---:|---:|
| 2026-10-31 04:06:46 | 17 | 28 | 59.3秒 / 5.88秒 |
| 2026-10-31 04:33:33 | 17 | 33 | 55.7秒 / 5.78秒 |
| 2026-10-31 04:43:49 | 14 | 25 | 47.7秒 / 5.92秒 |
| 2026-10-31 04:57:28 | 14 | 29 | 54.3秒 / 5.89秒 |
| 2026-10-31 05:09:09 | 10 | 51 | 52.1秒 / 5.84秒 |
| 2026-10-31 05:15:59 | 19 | 42 | 55.9秒 / 5.77秒 |
| 2026-10-31 05:27:02 | 21 | 48 | 60.9秒 / 5.78秒 |

最新スナップショットの待ち取得時間は約1.03秒から5.78秒だった。待ちログは約1秒で継続待ちを記録し、取得後にも記録するため、上表の待ち件数は待ち回数や全待ち件数ではない。ロック文の記録数・時間は100ms未満を含まず、複数接続の時間を足した値なので、壁時計時間やCPU時間として解釈できない。スナップショットは別々の観測であり、相互に加算していない。

同じ最新ログでは、`users` relationへの `RowExclusiveLock` 待ちも4件あり、いずれも `UPDATE users` だった。取得までの時間は1.216–1.698秒、合計5.813秒（並列セッションの合算）。SQLのSET列はbio 1件、profile_image_id 2件、account_type 1件。bioとプロフィール画像変更は採点対象のプロフィール変更機能に対応するが、各HTTP要求が実際にベンチマーカーの加点対象だったかは未確認。account_type更新は管理者によるユーザー種別更新で、採点効果を示すものではない。

## 採点区間での分類

採点区間は、nginxログ上で最初の投稿期間開始が成功した時刻（2026-10-31 05:25:16 UTC）を境界として便宜的に分けた。初期化完了やベンチマーク内部の厳密なフェーズ境界そのものではない。PostgreSQLログの最新スナップショットには、境界前の `ShareRowExclusiveLock` 待ち取得が7件、境界後が14件ある。境界後14件の待ち時間合計は21.717秒、最大2.920秒（05:26:11.506 UTC）。合計は並列待ち時間であり、経過時間ではない。

境界後の14件中、1件は期間開始処理の有力候補である。PID 72636は05:26:08.456 UTCに約1秒の継続待ちが記録され、05:26:10.088 UTCに総2.634秒でロックを取得した。その0.933秒後に同じPIDで `UPDATE posting_windows` が記録されている。このSQLは期間開始ハンドラー固有なので時系列は期間開始と整合するが、PostgreSQLログにtransaction IDやBEGIN/COMMITがなく、コネクション再利用による別transactionを排除できない。従って、期間開始要求自身が2.634秒待ったとは確定しない。残り13件も `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` の待ち取得までは確認したが、後続の登録／期間開始SQLは100ms未満で記録されないか、prepared statement名だけが記録されており分類できない。nginxでは登録要求も採点区間中に継続しているため、境界後の残りを全て期間開始要求または登録準備だけとみなすことはできない。

同じ採点区間に `users` の `RowExclusiveLock` 待ちが4件あり、SQL本文から全て `UPDATE users` と確認できた。うち3件は採点対象のプロフィール変更機能に対応するSQL（bio 1件、profile_image_id 2件）、残る1件はaccount_type変更だった。05:25:39のbio/profile-image更新待ちのwait queueには、同relationで `ShareRowExclusiveLock` を待っていたPID 72627が含まれる。05:26:11のaccount_type更新待ちではholder一覧にPID 72636が含まれ、そのPIDでは同時刻帯に `UPDATE posting_windows` も記録されている。ただしholder一覧はロックモードを示さず、PIDが同じでも同一transactionとは限らないため、期間開始transactionが更新を直接塞いだとまでは確定できない。少なくとも採点区間にSRE要求がqueueへ入り、プロフィール変更の `UPDATE users` が1秒超待ったことはログで確認できる。

**判定: 実待ちはあり、holder PIDも記録されている。** 代表例の2026-10-31 05:24:54.681 UTCではPID 72455がrelation 16684の `ShareRowExclusiveLock` を合計1.540秒待ち、同時刻の `DETAIL` にholding PID 72457ほか7つとwait queueが記録されている。約1秒の継続待ちログは同時刻に出ており、その後05:24:55.221 UTCに合計1.540秒で取得した。PID 72457には169ms前の05:24:54.512 UTCに `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` が完了した記録があり、durationは831.525msだった。このためPID 72457はSRE保持者の有力候補だが、ログにtransaction IDやBEGIN/COMMITがなく、backend接続再利用を排除できないため、holder一覧に載ったPIDと当該ロック文が同一transactionとは確定できない。

一方、`Processes holding the lock` の一覧だけでは全PIDのロックモードは分からず、互換モードの保持者も列挙され得る。PID 72457の `LOCK TABLE users ...` 記録は、登録と期間開始の両ハンドラーが発行する同一SQLであり、どちらの処理だったかはその後のSQL分類が必要。該当時刻の後続ログはprepared statement名と実行時間のみで、SQL本文が記録されていない。さらにstatementの記録閾値は100msで、短いSQLやtransaction境界も欠ける。このため、ログから登録・期間開始のどちらがholderだったか、他のholderのモード、個々のロック保持開始・終了は確定できない。ロック文の実行時間には獲得待ちが含まれるため、それを保持時間とみなすのも誤り。期間開始の通知INSERTやプロフィール変更との対応付け、接続プール待ち・採点活動・得点への波及は未計測である。

従って、採点区間にプロフィール変更機能に対応する `UPDATE users` が1秒超待ったことは確認でき、ロック競合がスコアへ影響する機序は現実的な候補である。ただし各要求が加点対象だったか、検証失敗や加点取りこぼしに結び付いたか、得点差はいずれも未確認で、改善量を断定できない。期間開始候補のロック待ちも見えたが、PIDだけで同一transactionと確定できず、`starts_at` はロック取得後に設定されるため、仮にその期間開始要求自身の待ちでも待ち時間が投稿期間の期限を直接消費するとは限らない。改善候補としては、採点区間に `UPDATE users` を待たせるSREロックの発生頻度・保持時間を下げる制御に着手価値がある。登録時のusername重複防止と期間開始時の整合性・通知作成を維持する代替制御が必要で、ロック削除だけの変更は推奨しない。

スコア改善を判定するには、同条件baselineでプロフィール更新の成功率・加点到達率と得点を比較し、DBのwait時間・接続プール待ちも併記する。併せてholder PIDごとのロックモードと操作カテゴリ、ロック保持開始・終了、期間開始処理と通知INSERTの時間、採点経路の期限超過を同じ時刻軸で記録する。既存ログはholder PIDを示すが、100ms未満SQLとtransaction境界が欠けるため、全待ちを登録か期間開始かに分類したり、保持時間や得点影響を確定するには足りない。

再集計は各スナップショットを個別に読み、待ち・取得行と直後の `DETAIL` のholder / wait queue PID、relation OID、同PIDのSQLカテゴリを照合する。holder一覧にあるだけで競合するロックモードと断定せず、ロック文の明示記録やロックモード情報と合わせて評価する。SQL全文や接続元を表示・保存しない。ログはGit管理外の機密ログとして扱う。

ISUCON10 `isucon10-case-035`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon10.md`）はprocess内mutexがmulti-host/multi-processの排他を共有しない例を記録している。資料はこのcloneに含まれず、本文を再確認できない場合がある。本件の競合や最適解を示すものではないが、DBロックをローカル排他で置き換える案は全アプリホストの競合者を覆うか確認する。

## 検証・受け入れ条件

競合がある/ない/未確定を根拠とともに記録する。変更する場合はユーザー重複防止・地域更新・期間開始を並行検証し、初期化と再起動後の状態も確認する。調査のみで閉じる場合も未確認事項を記す。

## 登録側の対策

`users.username` を全アカウント（退会済みを含む）で一意にする。新規DBの定義は [`001-schema.sql`](../webapp/sql/001-schema.sql)、既存DB向けは [`004-users-username-unique.sql`](../webapp/sql/004-users-username-unique.sql) に記録した。登録APIは一意indexを使う `INSERT ... ON CONFLICT (username) DO NOTHING RETURNING ...` で競合を409 `username_taken` とし、`LOCK TABLE users`、重複事前SELECT、登録後のユーザー再SELECTを行わない。ユニーク制約がまだない場合、`ON CONFLICT` はDBエラーとなり、重複登録を許さず500で失敗する。

既存DBへの適用は各SQL文をautocommitで実行する。先に重複usernameを調査し、重複があれば行を削除・統合せず作業を止める。重複がない場合も、`CREATE UNIQUE INDEX CONCURRENTLY` が失敗後に無効indexを残した可能性を確認する。再試行前に `pg_index.indisvalid` と `pg_get_indexdef` で同名indexの有効性・定義を確認する。重複による失敗で無効indexが残っていれば、DB管理手順で定義と依存を確認したうえでその無効indexだけを除去してから重複を明示的に解決し、再実行する。重複行は削除・統合しない。index作成が成功しattachだけが失敗した場合は、有効で定義も一致するindexを確認して `ALTER TABLE ... ADD CONSTRAINT ... USING INDEX` のみを再試行する。別定義または状態不明の同名indexがあれば作業を止める。indexを作成した後、短い `ALTER TABLE` でunique constraintとしてattachする。これは本番環境へ未適用であり、実際の適用時間やblockingは未計測。

配備時はDB migrationを先に適用し、その後Goアプリを3台へ配備する。既存のGo deployはSQLを適用しない。旧アプリへ戻す場合もunique constraintは残せるので、アプリrollbackと同時にDB制約を落とさない。DB制約の撤去が別途必要なら、旧アプリがテーブルロックによる重複防止を全稼働先で行うことを確認した後の別作業とする。

PostgreSQL opt-in統合テストは `ISUREAL_TEST_DATABASE_DSN` に指定したテストDBの専用schemaを作成し、migration、同名同時登録、退会済みusernameの予約、ログイン互換性、未適用時のfail-fast、登録・地域変更と期間開始の双方のlock順を検証する。初期化はテスト専用schemaを削除するだけで、通常・本番DBを変更しない。実際のアプリ配備・負荷試験は未実施のため、スコア効果は結論していない。期間開始側のテーブルロックによるユーザー更新待ちと、その待ちを減らす方法は引き続き調査対象。

### 初期化・永続性と回帰テストの確認（隔離fixture）

隔離PostgreSQL 16に `001-schema.sql` を適用し、架空の初期ユーザー、`003-initial-snapshot.sql`、架空の追加ユーザーとregion変更を用意して `reset.sql` を実行した。初期ユーザーだけが残りregionが初期値へ戻ること、username unique constraintが残ること、同名追加がunique violationになることを確認した。同じ隔離コンテナをrestartした後にも同じreset検証を再実行し、データと制約が維持された。実競技DB、初期ユーザー情報、実サービスの配備状態はこの確認の対象外。

テスト感度は、別の一時コピーで登録handlerだけを旧実装へ戻して `TestRegisterDoesNotWaitForAnotherUsersInsert` を実行して確認した。未commitの別ユーザーINSERT中に別名の登録が完了しないという契約違反でテストが失敗し、現行実装ではPostgreSQL統合テストを通過した。score改善と本番移行後の効果は未検証。
