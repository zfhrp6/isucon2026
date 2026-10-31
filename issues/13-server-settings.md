# アプリ・DB・nginx間の設定候補を実測する

## 採点への寄与

設定候補は主に応答時間と完了率を通じて加点へ寄与する仮説で、単独の加点保証はない。特に投稿後の友達フィード検証は通常期間10点、最終期間20点で、米州・欧州は1.5倍となる。画像取得は1回2点。FAILは0点なので、性能より先に仕様・初期化・永続性を守る。配点・閲覧上限は[公式マニュアル](../official_resource/manual.md#スコアの計算)を参照。

## 観測した事実と未確認事項

2026-10-31の読み取り専用調査で確認した範囲を記録する。実機の設定変更・負荷試験は行っていない。

- asia1、america1、europe1はGo/nginx、asia2は共有PostgreSQL 16.15。全アプリの`ISUREAL_DB_HOST`はasia2を指す。3回のping平均RTTはasia1 1.053ms、america1 100.846ms、europe1 100.847ms。ホストの`tc`にnetem設定はなかったが、ルーターやリージョン間の遅延・帯域制限は競技条件として変更・回避しない。
- 配備Go 1.26.5の[`server.go`](../webapp/go/server.go#L34)は`SetMaxOpenConns(30)`のみ設定し、`SetMaxIdleConns`は設定していない。[database/sqlの既定](https://pkg.go.dev/database/sql#DB.SetMaxIdleConns)idle上限2はソースで確認した。3アプリの最大接続数合計は90、DBの`max_connections`は100（reserved 3）。実際の接続廃棄数、`DBStats.WaitCount`、idle/active接続数は未計測。
- 配備nginxは全台1.24.0。[API設定断片](../ops/config/nginx/isu-asia1/isureal.conf#L8)はHostヘッダーと`proxy_pass`を設定するのみで、upstream keepalive・HTTPバージョン・Connectionヘッダーを指定していない。nginx既定ではproxy upstreamに持続接続を使わない（[upstream keepalive](https://nginx.org/en/docs/http/ngx_http_upstream_module.html#keepalive)、[proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_http_version)）。接続再確立の時間・頻度は未計測。
- asia2はRAM 3790 MiB、PostgreSQLデータ約16 GB、`shared_buffers=128MB`、`effective_io_concurrency=1`、`random_page_cost=4`、`work_mem=4MB`。現在の`temp_files`/`temp_bytes`累積は0。累積キャッシュ統計と調査時のPSI/vmstat/iostatは確認し、資源状態はほぼidleだった。負荷中のメモリ圧・I/O待ちとの同時測定はしていない（[PostgreSQL resource settings](https://www.postgresql.org/docs/16/runtime-config-resource.html)）。
- 実DBの索引一覧では`users.username`、`login_sessions.token_hash`、`realmojis.image_id`の索引がない。ダミーIDを使った`EXPLAIN (FORMAT JSON)`（`ANALYZE`なし）の実行計画には`realmojis`のSeq Scanが含まれた。実行頻度・実行時間・本番データでの選択性は未確認。
- `ISUREAL_LOG_LEVEL`は全アプリで未指定、配備既定値はdebug。journalでGETのdebug記録を確認し、ログガードも稼働していた。infoに変えた場合のログ量・必須情報への影響は未確認。
- nginxの`proxy_read_timeout`は未指定で既定60秒（[proxy_read_timeout](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_read_timeout)）、通知SSE実装にheartbeatはない。asia1のerror logにはtimeout 94件、そのうち02:20–02:39 UTCにnotificationsが15件あった。負荷フェーズとの厳密な対応や通知失敗との因果は未確定。マニュアル上、負荷テスト終了後に強制切断された未完了リクエストは採点・検証に影響しない（[負荷走行](../official_resource/manual.md#負荷走行)）。
- 02:24–02:27 UTCのnginx access logでは、全status混在のupstream時間平均はfriends feedがasia 0.2734秒、america 1.4872秒、europe 1.592秒、投稿POSTがasia 1.237秒、america 3.147秒、europe 3.4518秒だった。初期検証等を含み得る窓で、同条件のリージョン比較でもSQL時間でもない。旧issues記載の02:06付近の観測値と混ぜない。SSEの長いreqtimeは接続待ちを含み、通常APIの処理時間やCPU時間として扱わない。

調査時点でCPU/memory limit、FD、OOM/swap、ディスク枯渇、キュー溢れに達した兆候はなかった。負荷中の飽和有無は未確認。`worker_connections=768`も枯渇未確認。WAL checkpoint警告は前日のデータ投入時のみで、今回負荷の証拠ではない。

## 優先する候補

順位は調査優先度であり、実測スコア差ではない。

| 優先 | 候補 | 根拠と次の確認 |
|---:|---|---|
| 高 | DB idle poolを比較する | 往復RTTが遠隔リージョンで約101ms、idle上限既定2。`MaxOpenConns(30)`は維持したまま`MaxIdleConns` 8/16等を比較し、接続再作成・待ち・DB接続数・メモリを計測する。値は候補で、最適値未測定。 |
| 高 | 頻出キーの索引を検討する | `token_hash`は認証入口、`username`はlogin等、`realmojis.image_id`は画像認可経路に関係する。代表データで実行計画・実行時間・書込み/初期化コストを確認する。関連する[セッション検索](03-session-lookup.md)、[画像読み出し](06-image-read.md)、[クエリ索引](08-query-indexes.md)を参照。 |
| 中・低リスク | nginx upstream keepaliveを比較する | API proxyで持続接続設定がない。nginx upstream keepaliveを構成し、proxy HTTP/1.1とConnectionヘッダーも含め正しく再利用できるか確認する。nginx設定変更後は設定断片を読み込む主設定で`nginx -t`する。 |
| 中・実測要 | PostgreSQL `shared_buffers`を比較する | 128 MiBはDBホストRAMに対して小さめ。調査時の資源状態はほぼidleで、負荷中のキャッシュ不足・I/O待ちが支配要因かは未確認。512 MiB–1 GiBを候補として、OS cache、接続ごとのメモリ、DB応答時間を含め比較する。 |
| 条件付き | debugログをinfoへ下げる | GET debug記録とjournal出力の費用はあり得るが、実際のI/O比率は未測定。ログガード・必要ログ・調査可能性を保ち、負荷とログ要件を確認した場合のみ比較する。 |
| 条件付き | SSE timeoutとheartbeatを見直す | 60秒timeoutとheartbeatなし、通知timeout記録がある。負荷中に採点対象通知が失われ再接続負荷を生んだと相関できる場合に限り、read timeout延長とheartbeatを比較する。timeoutを削除するだけの対応はしない。 |

## 後回しにする候補

- `work_mem`増加：現在temp spillの証拠なし。接続数と同時実行数によるメモリ増を先に評価する。
- WALサイズ/頻度、JIT、`effective_io_concurrency`、`random_page_cost`：現在の負荷で不適切と示す証拠がない。計測なしの変更は後回し。
- worker接続枠、gzip/JSON圧縮：飽和やCPU・帯域の比較証拠なし。圧縮はCPU費用も測ってから判断する。

## 制約・受け入れ条件

- 変更前後を同じデータ状態・同じ負荷条件で比較する。総スコアだけでなく、投稿後フィード検証の成功率、リージョン別スコア、API p50/p95、DBクエリ/待ち/接続統計、ログ量、エラー・timeoutを記録する。実測したスコア差がなければ候補の順位は仮説のまま。
- 全リージョンで書込み直後に読み取れ、公開範囲・関係変更は即時反映する。画像URL認可を保ち、削除後5秒を過ぎた画像を返さない。再起動後もデータが残り、初期化・最終確認が成功する。
- 競技の地域・ネットワーク条件を変更・回避しない。DB同期耐久性、監視、必要なログやtimeoutを弱めない。
- FAILは0点。timeoutのうち負荷終了後の強制切断は採点対象外であるため、SSEエラー件数だけを理由に設定変更しない。
