# usersテーブル/行ロックの競合範囲を調べる

## 管理状況

- 状態: **調査中（users行・テーブルロックの遅いSQLを観測、阻害関係と採点寄与は未確認）**
- 対応範囲・完了条件: 登録・投稿期間開始時のテーブルロックと、API transaction内のusers行ロックの待ち・阻害範囲を切り分ける。
- 実装コミット/記録: usersロックの変更なし。05:13–05:16 UTCと[05:52:00,05:55:00) UTCの保存ログを確認。投稿期間ロック共有化案は[issue 14](14-posting-window-lock-investigation.md)を参照。
- 検証済み/未検証: [05:52:00,05:55:00) UTCのログには、`log_lock_waits` によりusers行ロック取得までの待機が61件（記録待機時間合計103.029秒、最大3.880秒）、テーブルロックが21件（55.675秒、最大6.024秒）記録されている。これは `deadlock_timeout=1s` を超えてログに記録された待機であり、100msは別途duration集計の閾値。累積ログの複数走行・初期検証の混在、採点区間・artifact・結果スコアとの対応、個別のblocking関係は未確認。duration集計を純粋な待ち時間とは扱わない。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [14 投稿期間ロック競合](14-posting-window-lock-investigation.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`registerHandler`](../webapp/go/user_api.go#L93) と[`openPostingWindowHandler`](../webapp/go/admin_posting_window_api.go#L25) は `users` に `SHARE ROW EXCLUSIVE` ロックを取得する。加えて複数のAPI transactionがusers行を`FOR NO KEY UPDATE`する。関係変更ヘルパー[`beginRelationshipTx`](../webapp/go/relationship.go#L30)は主体と対象の行を順にロックする。コメント作成も両ユーザーをロックして認可SQLを実行するが、この経路では画像を書き込まない（[`comment_api.go`](../webapp/go/comment_api.go#L31)）。RealMoji作成は[`beginRelationshipTxWithActor`](../webapp/go/relationship.go#L51)を通じて両ユーザーをロックし、認可SQLと画像書き込みを行ってからcommitする（[`realmoji_api.go`](../webapp/go/realmoji_api.go#L35)）。ログインも秘密鍵を検証する前にusername該当行をロックする（[`user_api.go`](../webapp/go/user_api.go#L136)）。登録/期間開始のテーブルロックと、これらの行ロックは別経路なので、競合関係を分けて調べる。issue 14は`posting_windows`行ロックが対象。

投稿作成は先にusers行をロックし、その後`posting_windows`行を`FOR UPDATE`する（[`post_api.go`](../webapp/go/post_api.go#L96)、[`post_api.go`](../webapp/go/post_api.go#L107)）。そのため投稿期間ロックを待つtransactionがusers行ロックを保持し、同じ利用者の別操作へ待ちを波及させる連鎖が考えられる。これはコードから分かるロック順序に基づく仮説で、保存ログから個別blockerやこの連鎖が実際に起きたとは確認できていない。

[05:52:00,05:55:00) UTCに限定した `logs/postgresql/20261031T055547Z-WOSEwV/isu-asia2/postgresql.log` のログ突合では、`log_lock_waits` によるusers行ロック取得までの待機が61件、テーブルロックが21件記録されている。いずれも `deadlock_timeout=1s` を超えた待機で、記録された待機時間の最大はそれぞれ3.880秒、6.024秒。100msはduration集計の閾値であり、この待機件数には適用していない。スナップショットは複数時間帯・走行を含み、初期検証や実負荷との帰属、blocking API、稼働artifact、採点への影響は未確認。

## 調査・制約

- 遅いSQL記録を特定の競合と同一視せず、ロック取得待ち・保持時間、blocking関係、同時登録・期間開始・ユーザー行をロックするAPIの成功率を同時刻で観測する。ログ上の `still waiting` と `acquired` は同一待機の対として数え、累積ログ内の別走行を一つの負荷結果にまとめない。
- 計測前にテーブルロックを削除・弱化しない。username UNIQUEを検討するなら既存重複確認に加え、同時登録、region変更、期間開始との整合性を検証する。

ISUCON10 `isucon10-case-035`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon10.md`）はprocess内mutexがmulti-host/multi-processの排他を共有しない例を記録している。資料はこのcloneに含まれず、本文を再確認できない場合がある。本件の競合や最適解を示すものではないが、DBロックをローカル排他で置き換える案は全アプリホストの競合者を覆うか確認する。

## 検証・受け入れ条件

競合がある/ない/未確定を根拠とともに記録する。変更する場合はユーザー重複防止・地域更新・期間開始を並行検証し、初期化と再起動後の状態も確認する。調査のみで閉じる場合も未確認事項を記す。
