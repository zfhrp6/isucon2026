# アプリ・DB・nginx間の設定候補を実測する

## 管理状況

- 状態: **調査中**
- 対応範囲・完了条件: 実機設定候補を負荷時指標と同条件比較で絞る。完了条件はスコア/成功率・資源指標と設定差の記録。
- 実装コミット/記録: このissueの当初の読み取り専用実機調査では設定変更をしていない。後続の現HEAD実装とissue [25](25-db-connection-reuse.md)のidle 8/16比較走行は別記録。
- 検証済み/未検証: 当初の読み取り調査と後続のidle比較走行を記載。現HEADの地域別open上限は未比較で、設定が最新実機へ配備済みかも未確認。
- 期待効果: **中（推定）**。実測効果: **未計測**。
- 関連issue: [03 session lookup](03-session-lookup.md), [06 image read](06-image-read.md), [07 notification polling](07-notification-polling.md), [08 query indexes](08-query-indexes.md), [23 デプロイ来歴](23-deployment-provenance.md), [25 DB接続再利用](25-db-connection-reuse.md)

## 採点への寄与

設定候補は主に応答時間と完了率を通じて加点へ寄与する仮説で、単独の加点保証はない。特に投稿後の友達フィード検証は通常期間10点、最終期間20点で、米州・欧州は1.5倍となる。画像取得は1回2点。FAILは0点なので、性能より先に仕様・初期化・永続性を守る。配点・閲覧上限は[公式マニュアル](../official_resource/manual.md#スコアの計算)を参照。

## 観測した事実と未確認事項

共通のrun形式と手軽な計測方法は[性能改善候補の計測ガイド](measurement-guide.md)を使う。nginx LTSVは正確な負荷窓で切り出し、`reqtime`と`apptime`、通常APIとSSEを分ける。現行のGo pool統計・PostgreSQLログを先に利用し、OS samplerやprofileの利用可否は実行環境ごとに確認する。

2026-10-31の読み取り専用調査で確認した範囲を記録する。実機の設定変更・負荷試験は行っていない。

- asia1、america1、europe1はGo/nginx、asia2は共有PostgreSQL 16.15。全アプリの`ISUREAL_DB_HOST`はasia2を指す。3回のping平均RTTはasia1 1.053ms、america1 100.846ms、europe1 100.847ms。ホストの`tc`にnetem設定はなかったが、ルーターやリージョン間の遅延・帯域制限は競技条件として変更・回避しない。
- 当初調査時点の配備Go 1.26.5では[`server.go`](../webapp/go/server.go#L34)が`SetMaxOpenConns(30)`のみ設定し、`SetMaxIdleConns`未指定だった。[database/sqlの既定](https://pkg.go.dev/database/sql#DB.SetMaxIdleConns)idle上限2、3アプリ合計のopen上限90、DBの`max_connections=100`（reserved 3）を確認した歴史スナップショットである。これは現HEADの設定ではない。現HEADは[`server.go`](../webapp/go/server.go#L47)でopen上限、[#L48](../webapp/go/server.go#L48)でidle上限を設定する。実効値や最新実機への反映は別途確認する。
- 配備nginxは全台1.24.0。[API設定断片](../ops/config/nginx/isu-asia1/isureal.conf#L8)はHostヘッダーと`proxy_pass`を設定するのみで、upstream keepalive・HTTPバージョン・Connectionヘッダーを指定していない。nginx既定ではproxy upstreamに持続接続を使わない（[upstream keepalive](https://nginx.org/en/docs/http/ngx_http_upstream_module.html#keepalive)、[proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_http_version)）。接続再確立の時間・頻度は未計測。
- asia2はRAM 3790 MiB、PostgreSQLデータ約16 GB、`shared_buffers=128MB`、`effective_io_concurrency=1`、`random_page_cost=4`、`work_mem=4MB`。現在の`temp_files`/`temp_bytes`累積は0。累積キャッシュ統計と調査時のPSI/vmstat/iostatは確認し、資源状態はほぼidleだった。負荷中のメモリ圧・I/O待ちとの同時測定はしていない（[PostgreSQL resource settings](https://www.postgresql.org/docs/16/runtime-config-resource.html)）。
- 実DBの索引一覧では`users.username`、`login_sessions.token_hash`、`realmojis.image_id`の索引がない。ダミーIDを使った`EXPLAIN (FORMAT JSON)`（`ANALYZE`なし）の実行計画には`realmojis`のSeq Scanが含まれた。実行頻度・実行時間・本番データでの選択性は未確認。
- `ISUREAL_LOG_LEVEL`は全アプリで未指定、配備既定値はdebug。journalでGETのdebug記録を確認し、ログガードも稼働していた。infoに変えた場合のログ量・必須情報への影響は未確認。
- nginxの`proxy_read_timeout`は未指定で既定60秒（[proxy_read_timeout](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_read_timeout)）、通知SSE実装にheartbeatはない。asia1のerror logにはtimeout 94件、そのうち02:20–02:39 UTCにnotificationsが15件あった。負荷フェーズとの厳密な対応や通知失敗との因果は未確定。マニュアル上、負荷テスト終了後に強制切断された未完了リクエストは採点・検証に影響しない（[負荷走行](../official_resource/manual.md#負荷走行)）。
- 02:24–02:27 UTCのnginx access logでは、全status混在のupstream時間平均はfriends feedがasia 0.2734秒、america 1.4872秒、europe 1.592秒、投稿POSTがasia 1.237秒、america 3.147秒、europe 3.4518秒だった。初期検証等を含み得る窓で、同条件のリージョン比較でもSQL時間でもない。旧issues記載の02:06付近の観測値と混ぜない。SSEの長いreqtimeは接続待ちを含み、通常APIの処理時間やCPU時間として扱わない。

上記のプール値、索引一覧、timeout集計は当初調査時点の実機記録であり、現HEADのコード・設定や後の実機状態とは区別する。実DBカタログと実行計画の保存記録は、画像逆引き索引を現HEADへ統合する前のもの。現HEADではidle16、地域別open上限Asia40 / Europe25 / America25、DBプール統計ログ機能、画像逆引き索引が統合済み。issue 25にはidle 8/16の走行記録があるが、地域別open上限はその走行後に統合され、比較効果は未計測。これらの現HEAD設定が最新実機へ配備されたかは別途確認する。

### 追加ログ・現HEADとの照合（2026-10-31 05:13–05:16 UTC）

最新保存ログ `logs/nginx/20261031T051553Z-Z9ANpD` と `logs/postgresql/20261031T051559Z-xTL8bT` はGit管理外。PostgreSQLログは05:13:32–05:16:00 UTC、nginxアクセスログは各ホストで05:13:41–05:15:44 UTCを含む。ファイル全体には初期検証等が含まれ、負荷フェーズ、実行中artifact、総スコアへの対応は独立確認できない。

PostgreSQLの100ms以上の`duration`付き`execute`/`statement`記録をSQLパターン別に集計すると、`posting_windows` の `FOR UPDATE` は274件（平均1.056秒、最大5.174秒、duration合計289.307秒）、users行の`FOR NO KEY UPDATE`は254件（0.584秒、3.492秒、148.225秒）、`LOCK TABLE users`は42件（1.332秒、5.768秒、55.937秒）だった。これは閾値以上の記録だけの集計であり、全クエリの平均ではない。statement durationには待ち以外の時間も含み、合計は並列実行を含むため経過時間・CPU時間ではない。ログの`still waiting`と`acquired`は各169件で、同じ待機の開始・取得記録の対として扱う。個々のSQL種別への帰属、採点区間のblocking関係、得点への寄与は未確認。

同じ集計ではfriends-of-friendsフィードSQLが82件（平均393.831ms、最大1,576.799ms）、他のフィード候補SQLが40件（平均503.206ms、最大2,419.019ms）、友達の友達取得SQLが131件（平均150.728ms、最大299.232ms）。DBのSQL別遅延が観測されたためissue [14](14-posting-window-lock-investigation.md)と[18](18-users-table-lock-investigation.md)を調査中へ進めた。ただし、集計だけではSQL削減・ロック変更の得点効果を確定しない。

同一スナップショットのHTTP成功応答は、friends feedのreqtime平均/p95がアジア0.654/1.844秒（525件）、米州1.095/1.460秒（41件）、欧州1.013/1.456秒（59件）。投稿はアジア1.608/3.302秒（267件）、米州2.090/4.630秒（39件）、欧州2.693/5.489秒（46件）。初期検証等を含むため性能比較のbaselineではない。最新窓のerror logにはSSE upstream timeoutはなかった。過去のログ記録を最新窓に混ぜず、SSEのreqtimeは接続待ちを含むものとして扱う。

### 現HEADで確認した未計測候補

- 現HEADの[`user_read_api.go`](../webapp/go/user_read_api.go#L29)はカーソル後の有効ユーザーをSQLで全件取得した後、Goで名前検索する。DB取得行数は未計測。詳細は[issue 04](04-user-search.md)。
- [`memory_api.go`](../webapp/go/memory_api.go#L105)はMemories内で投稿ごとに50件だけ返す交流をSQLから全件取得してGoで切り詰める。最新保存nginxログのMemories成功応答はリージョン別平均0.480–0.650秒だが、SQL行数・時間の寄与は未計測。[issue 05](05-interaction-reads.md)を参照。
- [`notification_api.go`](../webapp/go/notification_api.go#L37)はSSE接続ごとに500ms間隔でユーザーと通知のSELECTを行う。接続数に応じるSQL仕事量と通知遅延を計測する（[issue 07](07-notification-polling.md)）。
- [`image_signature.go`](../webapp/go/image_signature.go#L24)は画像URL署名あたり1,000回のHMAC計算をする。CPU・割当量を示すprofileは未取得で、画像DB読み出しの時間とは区別する（[issue 17](17-image-signature-cpu.md)）。
- 現HEADの[`server.go`](../webapp/go/server.go#L47)にはDBプール上限設定があり、統計ログと合わせてpool待ちとDBロック待ちは別の指標として記録する。地域別open上限の効果はidle16走行から推定しない（[issue 25](25-db-connection-reuse.md)）。

調査時点でCPU/memory limit、FD、OOM/swap、ディスク枯渇、キュー溢れに達した兆候はなかった。負荷中の飽和有無は未確認。`worker_connections=768`も枯渇未確認。WAL checkpoint警告は前日のデータ投入時のみで、今回負荷の証拠ではない。

## 優先する候補

DB接続のidle再利用は[issue 25](25-db-connection-reuse.md)、配備物と計測結果の対応付けは[issue 23](23-deployment-provenance.md)に調査目的を分ける。

順位は調査優先度であり、実測スコア差ではない。

| 優先 | 候補 | 根拠と次の確認 |
|---:|---|---|
| 高 | 現行DBプール設定を実効確認し比較する | HEADと共通設定はidle16、open上限Asia40 / Europe25 / America25。これらが稼働中3台に反映されているか起動時ログで確認する。idle16の単発走行はopen配分変更前であり、配分自体の効果は未計測。設定・初期状態を揃え、接続再作成、pool待ち、DB接続数・メモリ、スコアと実現処理件数を比較する（[issue 25](25-db-connection-reuse.md)）。 |
| 高 | 未確認の頻出キー索引と画像索引の実効性を確認する | `token_hash`は認証入口、`username`はlogin等で使われるが現HEADのDDLに索引定義がない。画像逆引き索引は現HEADへ統合済み。最新DBカタログと計画で各索引の有無・選択を確認し、読取時間と書込み/初期化コストを比較する。画像索引は未実装候補として重複計上せず、実DB反映と効果の確認を[issue 06](06-image-read.md)、[issue 26](26-postgresql-index-investigation.md)、[issue 03](03-session-lookup.md)に記録する。 |
| 中・低リスク | nginx upstream keepaliveを比較する | API proxyで持続接続設定がない。nginx upstream keepaliveを構成し、proxy HTTP/1.1とConnectionヘッダーも含め正しく再利用できるか確認する。nginx設定変更後は設定断片を読み込む主設定で`nginx -t`する。 |
| 中・実測要 | PostgreSQL `shared_buffers`を比較する | 128 MiBはDBホストRAMに対して小さめ。調査時の資源状態はほぼidleで、負荷中のキャッシュ不足・I/O待ちが支配要因かは未確認。512 MiB–1 GiBを候補として、OS cache、接続ごとのメモリ、DB応答時間を含め比較する。 |
| 条件付き | debugログをinfoへ下げる | GET debug記録とjournal出力の費用はあり得るが、実際のI/O比率は未測定。ログガード・必要ログ・調査可能性を保ち、負荷とログ要件を確認した場合のみ比較する。 |
| 条件付き | SSE timeoutとheartbeatを見直す | 60秒timeoutとheartbeatなし、通知timeout記録がある。負荷中に採点対象通知が失われ再接続負荷を生んだと相関できる場合に限り、read timeout延長とheartbeatを比較する。timeoutを削除するだけの対応はしない。 |

## 実測が得られた単独比較候補

[issue 27](27-postgresql-jit-comparison.md)の単発代表SQLではJIT offのexecution短縮を観測したが、cache状態が異なり得点/ベンチ効果は未計測。JITだけを切り替えた負荷比較候補として扱い、比較が終わるまでは永続設定を変更しない。

## 後回しにする候補

- `work_mem`増加：現在temp spillの証拠なし。接続数と同時実行数によるメモリ増を先に評価する。
- WALサイズ/頻度、`effective_io_concurrency`、`random_page_cost`：現在の負荷で不適切と示す証拠がない。計測なしの変更は後回し。
- worker接続枠、gzip/JSON圧縮：飽和やCPU・帯域の比較証拠なし。圧縮はCPU費用も測ってから判断する。

## 制約・受け入れ条件

- 変更前後を同じデータ状態・同じ負荷条件で比較する。総スコアだけでなく、投稿後フィード検証の成功率、リージョン別スコア、API p50/p95、DBクエリ/待ち/接続統計、ログ量、エラー・timeoutを記録する。実測したスコア差がなければ候補の順位は仮説のまま。
- 全リージョンで書込み直後に読み取れ、公開範囲・関係変更は即時反映する。画像URL認可を保ち、削除後5秒を過ぎた画像を返さない。再起動後もデータが残り、初期化・最終確認が成功する。
- 競技の地域・ネットワーク条件を変更・回避しない。DB同期耐久性、監視、必要なログやtimeoutを弱めない。
- FAILは0点。timeoutのうち負荷終了後の強制切断は採点対象外であるため、SSEエラー件数だけを理由に設定変更しない。
