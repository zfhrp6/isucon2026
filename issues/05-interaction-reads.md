# コメント・RealMoji・Memoriesの取得件数を抑える

## 管理状況

- 状態: **実装済み（ローカル検証済み、実機性能・スコア未計測）**
- 対応範囲・完了条件: Memories内の交流取得行数を契約どおり抑える。完了条件はRealMoji/コメントの件数・順序と負荷の検証。
- 実装コミット/記録: `9f9d16c`でコメント・RealMoji一覧のプロフィールURL再利用と各経路の51件目変換省略を実装。rebase中の`a0cc84c`でMemoriesの投稿別交流取得上限を追加。
- 検証済み/未検証: Memoriesの投稿ごとにRealMoji最新50件・コメント最古50件をSQLで取得する統合テストを追加。通常一覧とMemoriesで51件目を応答変換前に除外。実機の行数・転送量・性能・スコアは未計測。
- 期待効果: **中（50件超の交流がある投稿の割合に依存）**。実測効果: **未計測**。
- 関連issue: [08 query indexes](08-query-indexes.md)

## 採点への寄与

交流一覧は合計8回/利用者/投稿期間まで各1点。Memoriesは月単位3点、日単位2点。応答の小型化は閲覧完了率と共有DB負荷に寄与しうる。効果：**中（推定）**。

## 現状

[`listMemories`](../webapp/go/memory_api.go#L73) は最大50投稿を取得する。続く [`loadMemoryInteractions`](../webapp/go/memory_api.go#L105) は投稿ごとにRealMoji最新50件・コメント最古50件をSQLで取得する。単一投稿一覧の [`getRealMojisHandler`](../webapp/go/realmoji_api.go#L127) と [`getCommentsHandler`](../webapp/go/comment_api.go#L96) は、継続判定用51件目を応答変換前に除外する。

2026-10-31のnginx全ログ集計ではMemoriesの成功応答が、同じスナップショット内でアジア599件（平均0.480秒、p95 1.143秒）、米州76件（0.639秒、0.961秒）、欧州103件（0.650秒、0.975秒）だった。ログにはDB取得行数や当該SQLの時間がないため、全件取得の寄与・改善余地は未確定。保存ログ `logs/nginx/20261031T051553Z-Z9ANpD` は初期検証等を含む。

## CPU profileで確認した経路

2026-10-31のasia1 CPU profile活動4区間では、Memories月・日一覧handler合計35.57秒のうち署名stack35.19秒を観測した。これはSQL取得行数やdatabase waitではなく、応答オブジェクト作成中の画像URL署名CPUである。stack・時間窓・帰属条件は[issue 30](30-asia1-cpu-profile.md)、署名最適化の検証条件は[issue 17](17-image-signature-cpu.md)を参照する。行数を絞る案の寄与と混ぜない。

## 改善方針

### 継続判定行の応答加工

DB取得範囲の削減とは別に、既存の各応答構築経路で51件目を変換前に除く。

| 実装箇所 | 不要な加工 | 維持する条件 |
|---|---|---|
| `comment_api.go`の`getCommentsHandler` | 返さないコメント著者のプロフィールURL生成 | 最古順、認可、`hasMore` |
| `realmoji_api.go`の`getRealMojisHandler` | 返さないRealMoji画像・利用者プロフィールのURL生成 | 最新順、有効状態、`hasMore` |
| `memory_api.go`の`listMemories` | 返さない51投稿目の投稿画像・プロフィールURL生成 | 投稿順、`hasMore`、交流を読む投稿集合 |

取得件数から`hasMore`を決めてから、返却する最大50件のみ変換する。SQLの51件取得は継続判定のため残す。各handlerは別々に境界件数0/50/51を確認し、行数削減や[応答内署名再利用](34-response-image-signature-reuse.md)の効果と混ぜず署名回数を比較する。

Memoriesの投稿ごとに返却契約上必要な50件だけをSQLで選ぶ。RealMojiは `created_at, id` の降順で最新50件、コメントは同じ列の昇順で最古側50件。投稿IDの一括取得を維持し、N+1クエリ化は避ける。51投稿目も応答変換前に除外する。

## 制約・リスク

- 応答上限50件、RealMojiの最新順、コメントの最古順、削除済み/置換済み除外、投稿者のactive状態を維持する。
- ページ内の投稿ごとに上限が適用される。全投稿をまとめたLIMITに置換すると後方の投稿の交流が欠落する。
- 初期履歴・画像・交流内容は初期化後も一致し、再起動後に取得できる必要がある。

## 検証・受け入れ条件

投稿が複数件あり、各投稿に55件の交流があるPostgreSQL統合テストで各投稿の件数と順序を確認する。0/50/51境界、実機の取得行数・転送量・応答時間、Memories採点活動の比較は未実施。
