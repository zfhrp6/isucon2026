# フィード候補取得を必要件数に限定する

## 管理状況

- 状態: **調査中**
- 対応範囲・完了条件: フィード候補をSQLで絞る設計と認可・順位の受け入れを確認する。完了条件は結果契約一致と同条件の到達率/スコア比較。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: ログとコード構造を確認。SQL別時間・EXPLAIN・性能/得点比較は未検証。
- 期待効果: **大（推定）**。実測効果: **未計測**。
- 関連issue: [02 viewer context](02-viewer-context.md), [08 query indexes](08-query-indexes.md), [15 空フィードの早期判定](15-empty-feed-short-circuit.md), [16 認可読み取りのスコープ](16-scoped-authorization-reads.md)

## 採点への寄与

フィード閲覧は1回1点、投稿後の友達フィード検証は通常期間10点・最後の期間20点。閲覧対象ユーザー数と期間あたりの上限を踏まえ、取得時間短縮が検証完了率を上げる可能性がある。効果：**大（推定）**。このクエリが実際の支配的要因かは未計測。

## ログ観測

`logs/nginx/20261031T020717Z-nA4kZQ` の各 `access.ltsv.log` 全体（負荷フェーズ限定ではない）でfriends feedの `reqtime` 平均はアジア0.225秒（101件）、米州1.612秒（10件）、欧州1.528秒（10件）。地域間遅延と共有DBへの逐次照会が仮説として挙がるが、ログにはSQL時間・回数がなく原因の切り分けはできない。リクエスト数が少ないため、ここから安定した地域別性能差も断定しない。ログはGit管理外。

## 現状

[`getFeedHandler`](../webapp/go/feed_api.go#L32) は [`selectPostsWithAuthor`](../webapp/go/post.go#L65) で `w.ended_at IS NULL` の投稿をページング取得するが、feed 種別・友達関係・投稿者ブロック・投稿期間などの絞り込みを Go 側で行う。通常フィードもSQLの `LIMIT` がなく、絞り込み後に最大51件で打ち切る。friends-of-friends の推薦は候補全体のスコア計算後に並べ替えるため、候補が増えるほど読み込み・集計・ソートが膨らむ。

## 改善方針

閲覧可能性と各フィード条件をSQLに移し、必要なページだけ取得する。friends-of-friends はページング順序と推薦スコア順を壊さず、スコア集計を候補集合に限定する。まずEXPLAINと候補行数を測り、インデックスとクエリ形を合わせる。

## 制約・リスク

- `viewerContext.canView` と `matchesFeed` の全条件を意味的に再現する。ブロック、投稿期間終了、本人投稿、投稿者状態、投稿した利用者だけが他人の投稿を閲覧できる条件を落とさない。
- 投稿・友達・ブロック等の書き込み直後は、他リージョンからの読み取りにも即時反映する。古いローカルキャッシュで代替しない。
- フィードのカーソル順、`hasMore`、JSON構造を維持する。friends-of-friends のスコア同点時の順序も変えない。
- `LIMIT 51` を単純にSQLへ移すだけでは、認可条件を満たす投稿が51件より後ろにある場合に欠落する。絞り込みと順位の後にlimitする。

## 検証・受け入れ条件

フィード種別ごとの既存ベンチ検証を通し、投稿後検証が全地域で成功する。権限境界、ブロック、期間終了、ページング先頭・次ページの応答が変更前と一致する。EXPLAIN、候補行数、DB時間とベンチスコアで効果を比較する。

## 実装と合成データでの検証

通常フィードは認可・種別条件をSQLで適用してから投稿日時とID順に51件を取得する。推薦フィードは軽量な投稿候補からスコアを集計し、スコア・投稿日時・ID順のカーソルと51件制限を適用してから投稿・著者の詳細を結合する。投稿者ごとの接続スコアは候補集合から投稿者IDを重複排除して集計する。

`webapp/go/feed_api_integration_test.go` は旧Go側の認可・フィード判定と推薦スコアをテストoracleとして保持し、一時PostgreSQL上で4種類のfeed結果を比較する。ブロック、inactive/deleted投稿者、終了期間、閲覧条件、エンゲージメント重複、推薦スコア上限、同点順、次ページカーソル、候補51件制限を含む。`ISUREAL_TEST_DATABASE_DSN` を指定した場合に実行され、未指定ならスキップする。

PostgreSQL 16の使い捨てDBで実際のフィードSQLに `EXPLAIN (ANALYZE, BUFFERS)` を実行した合成fixtureでは、publicの適格候補59件に対して51件を返した。publicのSQL実行時間は変更前0.795 ms、変更後0.830 msだった。friends-of-friendsは適格候補5件で、viewer mutual CTEを再利用する前の計画では候補判定中のNested Loop Semi Joinで5,464行を除外し、全体36.594 msだった。変更後は候補CTEが5行を作り、同じテストの2回の計測は1.481 msと0.986 msだった。測定回数・規模とも小さく、再現性や実性能改善を実証する値ではない。

fixtureは69件の投稿候補と78件のfriendship行を持つ小さなテスト用スキーマで、主キーとfriendshipの一意索引だけを持つ。競技データ量・本番相当の索引・リージョン間通信は再現しない。計測はSQL形状の変化を確認するためのもので、実データ性能の証明ではない。

再実行例: `ISUREAL_TEST_DATABASE_DSN='postgres://test:test@127.0.0.1:55432/test?sslmode=disable' go test -run TestSelectFeedPostsPostgres -v`。全ローカルテストは `go test ./...` で実行する。実データでのEXPLAIN比較、全地域の既存ベンチ、投稿後検証の地域横断結果は未実施で、改善効果は未確定。

## friends feedの負荷時タイムアウト調査

2026-10-31 03:07:59–03:08:28 UTCのログ集計窓（ベンチ終了03:08:23 UTC後の応答を含む）で、HTTP status 200のfriendsリクエストは計36件だった。内訳はアジア23件で平均12.812秒・最大20.484秒、米州7件で平均12.520秒・最大19.208秒、欧州6件で平均18.541秒・最大20.917秒。ベンチ全体では5/37件、13.5%が負荷開始から20〜25秒の相対区間で失敗した。ログ識別子は `logs/nginx/20261031T030854Z-dxGe53`。収集物は別checkout `/home/zfhrp/workspace/isucon2026/logs/nginx/20261031T030854Z-dxGe53` に存在し、このcheckoutにはない。対象時間帯に該当するnginx upstream errorは確認されず、reqtimeとapptimeが近く上流応答待ちが支配的だった。これは遅延の観測であり、nginx障害を示すものではない。

実環境のSQL実行時間は未取得だが、同じfriendsクエリで不要な二段階探索による遅延を合成データ上に再現した。今回のタイムアウトの有力原因としてこのクエリ形を修正した。PostgreSQL 16の合成fixture（10,000 users、120,841 friendships、300,000 posts、viewerの直接friends投稿30,000件、主キーとfriendship一意索引およびpostsのauthor/window索引）では、旧条件の `EXPLAIN (ANALYZE, BUFFERS)` は10秒のstatement timeoutに達した。推定コストは約2.29×10^9で、friends-of-friends探索を含むfilterのためだった。直接friendship条件を保ち可視性を `IN ('public','friends','friends_of_friends')` に限定した同一fixtureでは51行を返し、実行時間146.957 ms（planning 1.068 ms）だった。未絞り込み投稿・著者取得の比較値は299,970行、296.964 ms。計測は単発・合成データであり、実地域ベンチの修正後結果や競技データでの性能を証明しない。

互換性は `feed_api_integration_test.go` のPostgreSQL実行で旧Go側の `canView`/`matchesFeed` oracleと比較する。直接friendの3 visibility、非friendの同3 visibility、block両方向、inactive/deleted/window ended/self、先頭51件と次カーソルを検証する。局所再現は `/tmp/isucon-feed-load.sql` をPostgreSQL 16へ投入し、`statement_timeout='10s'` のまま `EXPLAIN (ANALYZE, BUFFERS)` の旧friends条件と簡約条件を比較する。実行ログは `/tmp/isucon-feed-fix-large-explain.log`。一時fixture scriptは作業環境内の再現用で、リポジトリには含めない。followingは今回変更しておらず、代表規模での一般可視性探索の性能は未検証。
