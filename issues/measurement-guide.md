# 性能改善候補の計測ガイド

性能改善候補は、同じ条件の走行で対象経路・資源・採点結果を結び付けて判断する。既存ログの観測値、ユーザー報告、そこから立てた原因・効果の仮説、ここに示す追加計測の提案を区別する。負荷窓や配備物との対応が未確認の値を、統制された走行の結果として扱わない。

## 走行ごとに残す共通情報

| 項目 | 記録内容 |
|---|---|
| run | 一意な`run_id`、地域、リクエスト数、得点、走行条件 |
| 時間窓 | 初期化・検証・負荷・終了をそれぞれUTCの半開区間`[start,end)`で記録。ログはこの区間で切り出し、累積ログ全体を一走行とみなさない |
| 実行物 | source tree/artifact/config digest、schema・index状態、統計の世代または前後スナップショット。秘密値は保存しない |
| 時刻 | 区間時間はmonotonic clockで測る。ホスト間のwall clock offsetも残し、UTC時刻だけから短い遅延を比較しない |
| リクエスト相関 | 必要ならnginxからGoへ同じ`request_id`を伝播する。集計labelは固定operation名、route、region等に限定する |

新規計測のoperation名は固定し、SQL本文、秘密、署名付きURL、生のuser IDをmetric labelや通常ログへ入れない。高カーディナリティの相関が必要な場合だけ、run内限定の匿名キーをtrace等の詳細記録に使い、集計labelへ持ち込まない。採点イベントの一部はbenchmark側でしか観測できないため、server logだけから成功を断定しない。

## 既存情報から始める計測

| 方法 | 使い方と読み方 |
|---|---|
| nginx LTSV + alp | 既存のaccess logを正確な負荷窓で切り出す。`reqtime`（nginxで見た総応答時間）と`apptime`（upstream応答時間）を別々に出す。UUIDを含むpathはroute単位に集約し、SSEは通常APIと分ける。末尾の累積ログ全体を走行として扱わない。収集方法は[nginxアクセスログの既存手順](../ops/config/README.md#nginx-アクセスログを-alp-で分析する)を参照。`access-load.ltsv.log`が対象負荷窓へ切り出し済みの場合、reqtimeで上位routeとpercentileを表示する例: `alp ltsv --file access-load.ltsv.log --apptime-label reqtime --sort sum --reverse --percentiles 50,95,99`。画像IDごとのpathを一つのグループにまとめるには`-m '^/api/images/[^/]+$'`を加える。`apptime`で分析するときは`--apptime-label reqtime`を外す |
| Go pool統計 | 現HEADのDB pool統計ログを使い、10秒間隔の前後差分からWaitCount/WaitDuration、接続数等を見る。集約された`DBStats`差分を個別requestへ割り当てない |
| PostgreSQLログ | 既存の`log_min_duration_statement=100ms`とlock loggingを使う。これは閾値以上のSQLの観測であり、全クエリ分布ではない。`still waiting`と`acquired`は一つの待機の対として集計する |
| OS資源 | `pidstat -u -r -d -h 1 120`、`iostat -x -y -t 1 120`でCPU、メモリ、I/Oを1秒間隔で記録する。導入済みとは仮定せず、実行ホストで利用可否を確認してから使う |
| Go profile | 30秒CPU profileを採り、allocsは走行前後のprofile差分で見る。process全体の割当差分を特定operationの回数で割ると、並行する他APIの割当が混ざる。operation固有のstack範囲をprofileで特定するか、対象処理だけの隔離比較で測る。heap profileは採取時点で保持されているheapで、累積割当量とは別。CPU例: `go tool pprof -top cpu.pprof`、`go tool pprof -http=127.0.0.1:8081 cpu.pprof`。CPU profileはDB待ちを示さない。RSS等を定期採取してもサンプル最大値は真のpeakを保証しない。profileの配備状態は各runの実効artifactで確認する |
| SQL統計 | `pg_stat_statements`が既に利用可能なら走行前後のsnapshot差分を取る。未ロードならshared preload設定と再起動が必要なので別途計画する。使えない場合は固定operation名ごとのアプリ側集計を使う |
| blocking調査 | `pg_stat_activity`と`pg_blocking_pids()`は疑わしい時間帯に限定して取得する。短いwaitはsnapshot間に終わり、取り逃すことがある。常時高頻度pollingは避ける |

既存のnginx LTSVには`remote_addr`や`request_uri`が含まれ、IPやquery string・署名付きURLが残る場合がある。取得した生ログは機密情報として扱い、共有する集計からIPとqueryを除き、匿名化したroute単位で表示する。

簡単な計測から始め、常設の大規模監視基盤は必要性が分かってから検討する。計測あり/なしの走行も比較し、負荷やログ量へのoverheadを確認する。

## issue別に判断する内容

| issue | 判断したいこと | 指標 | 取得方法・見せ方 |
|---|---|---|---|
| 14 投稿期間ロック | 共通行lockが投稿を直列化しているか | wait回数・時間、blocking相手、取得後からcommitまで、投稿成功/期限内検証 | PostgreSQL wait/acquired対と限定的blocking snapshotを対応付ける。lock待ちとtransaction保持時間を分ける |
| 18 usersロック | users行/table lockが独立した競合か、#14から波及したか | lock種別、wait時間、blocker、投稿処理との関係 | #14と同じ時間窓で分類し、閾値ログの件数を全wait数と誤認しない |
| 02 viewer context / 16 scoped auth | viewer情報の逐次読取が対象経路のDB往復を占めるか | operation別query数、SQL elapsed、取得行数、API p50/p95 | handler段階ごとにquery数・行数・elapsedを集計し、#02と#16の重複削減を別々に足さない |
| 15 empty feed | 空応答条件の残りで候補取得を避けられるか | 条件別request数、候補query数/rows、成功数 | 条件を分けて集計する。未投稿時早期応答の現HEAD実装は既対応として扱う |
| 03 session lookup | session照会/索引が認証経路に効くか | route別件数・時間、query rows、実DB index/plan | access logとSQL統計・DB catalog/planを同じartifact/DB状態に結び付ける |
| 04 user search | SQL取得後のGo側走査が過剰か | DB取得rows、返却rows、handler時間、CPU | 取得と返却を別々に数え、検索routeごとに比較する |
| 05 Memories | 必要数を超えた交流取得が費用を占めるか | query rows、返却rows、SQL/handler時間 | 投稿単位の取得数と最終返却数を分けて測る |
| 08 indexes / 26 index investigation | 索引が対象SQLで選択され、read/write tradeoffがあるか | plan、rows、query time、更新/初期化費用 | 実DB catalogと代表plan、負荷中のSQL統計を記録。画像索引は既統合範囲と混同しない |
| 21 write roundtrips | 残る書込経路のDB往復が応答を遅らせるか | operation別query数、時間、commit、成功率 | request内の固定operation集計と代表request waterfallを比較 |
| 06 image read / 29 image cache | 画像認可、DB取得、転送、cache再利用のどこが費用か | DB時間、bytes、同一run内hit/miss、API時間 | 識別子はrun内匿名化し、cold/warmを分ける。累積ログのID再出現率をhit率と呼ばない |
| 17 image HMAC | 署名計算がCPU/割当負荷を占めるか | CPU samples、割当量、画像URL数、成功request | 30秒CPU profileとallocs profileの走行前後差分を確認する。割当量をoperationへ帰属するには、該当stack範囲の特定または対象処理だけの隔離比較が必要。heap profileは保持量を見る |
| 22 JPEG寸法事前判定 | 寸法判定が対象入力の処理時間・失敗率を変えるか | 入力寸法bucket、処理段階時間、status/失敗数 | #12の502原因調査とは分け、秘密や画像内容を記録せず固定bucketで比較する |
| 12 プロフィール画像502 | どの段階で502が発生し、原因がどこにあるか | status、nginx/upstream所要時間、失敗段階、対象route | nginx error/access logとアプリ側の固定operation記録を同じ負荷窓で対応付ける。原因は未確定として分類し、#22のJPEG寸法判定と同一視しない |
| 11 export | exportのメモリ・CPU費用と通常APIへの干渉があるか | bytes、heap保持量、CPU、同時通常API latency | export有無の同条件走行でRSS/heapと通常route p95を時系列比較。RSSのサンプル最大は真のpeakではない |
| 07 SSE | pollingがDB負荷や通知遅延を増やすか | 同時接続、poll回数、query時間、通知受信時刻 | SSEを通常APIから分離。通知受信・採点完了はbenchmark側のeventも記録 |
| 25 connection pool | pool待ちとDB競合・資源のどちらが制約か | WaitCount/Duration、open/idle、DB接続数、lock wait、処理量 | pool設定別の同条件走行を比較。open上限増加をlock改善と解釈しない |
| 27 PostgreSQL JIT | JITが代表SQL/全体処理量に寄与するか | SQL時間、CPU、成功数、score、plan/JIT状態 | 同一初期状態・負荷でJITだけを切替え、局所SQLと全体指標を並記 |
| 13 middleware/settings | nginx・Go設定が接続、待ち、ログ等に効くか | reqtime/apptime、接続再利用、timeout、CPU/IO、ログ量 | LTSV・Go pool・OS/DB統計を共通runで比較し、設定が実機へ反映されたか記録 |

### 計測値の境界

SQL clientのelapsedには、通常のDB呼び出しではpool取得に要した時間が含まれる場合があり、通信、DB実行、結果scanも含む。transaction内の呼び出しは既に接続を確保しているため、pool取得時間を常に含むとは限らない。実装上の計測開始・終了点を確認し、rowsを読み終えてcloseするまで測る。これは純粋なDB実行時間でもlock wait単体でもない。実際に返った行数とDB内部で走査した行数も区別する。必要ならviewer段階ごとの時間/rows、Memoriesの取得数と返却数、lock取得SQLと取得後commitまでの時間を追加instrumentする。

Globalな`DBStats`差分をrequest単位へ割り当てない。p50/p95等のpercentileは個々の値から再計算し、percentile同士を足したり平均したりしない。親子stageの時間を同じ総時間に加算せず、nested計測の二重計上を避ける。snapshotで見えたlock待ちを全待機件数とみなさず、wait/acquiredの対を一回として扱う。

## 結果の見せ方

- 地域×APIの比較表にrequest count、success、p50/p95、queries/request、rows/requestを並べる。pidstat/pprofのCPUは地域×APIへ直接帰属できない。run/host全体のCPU秒÷同区間の成功処理総数は別指標として示し、特定APIのCPU/successは帰属可能な計測がある場合に限る。
- API latency、pool待ち、lock待ち、CPU/RSS/I/Oを共通時間軸の時系列で表示する。
- 通常requestと遅いrequestの代表例はstageごとのwaterfallで示す。観測していない段階は「未観測」とする。
- 採点経路は通知受信→投稿→画像/詳細取得→期限前feed検証のfunnelにし、各段階の到達数・失敗数・期限超過を示す。通知受信や採点成功がbenchmark側で未観測なら、その段階は未観測と明記する。
- counters/histogramsは全件集計し、詳細traceは上限付きの固定samplingにする。sampling率と計測有無によるoverheadを記録する。

## 一次資料

- [alp](https://github.com/tkuchiki/alp)
- [pidstat(1)](https://man7.org/linux/man-pages/man1/pidstat.1.html)、[iostat(1)](https://man7.org/linux/man-pages/man1/iostat.1.html)
- PostgreSQL 16: [pg_stat_statements](https://www.postgresql.org/docs/16/pgstatstatements.html)、[monitoring statistics](https://www.postgresql.org/docs/16/monitoring-stats.html)、[functions-info (`pg_blocking_pids`)](https://www.postgresql.org/docs/16/functions-info.html)
- [Go runtime/pprof](https://pkg.go.dev/runtime/pprof@go1.26.5)
- [nginx upstream variables](https://nginx.org/en/docs/http/ngx_http_upstream_module.html#variables)
