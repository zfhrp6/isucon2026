# 採点対象クエリに必要な複合索引を整える

## 採点への寄与

ページングや関係一覧など複数の読み取りに波及する可能性がある。効果：**中（推定）**。索引を追加する前に実データ量と実行計画を確認する。

## 現状

[`001-schema.sql`](../webapp/sql/001-schema.sql#L154) の投稿索引は `author_id` と `posting_window_id` 単列で、フィード/履歴では `posted_at,id` の複合カーソルを使う。コメントは `post_id` 単列索引だが一覧は `created_at,id` 順（[`comment_api.go`](../webapp/go/comment_api.go#L96)）。RealMojiも投稿ID単列索引だが `created_at,id` 順（[`realmoji_api.go`](../webapp/go/realmoji_api.go#L127)）。フォローの逆引きは `followee_id` を使う一方、スキーマの一意制約は `(follower_id,followee_id)`（[`relationship_list_api.go`](../webapp/go/relationship_list_api.go#L37)、[`001-schema.sql`](../webapp/sql/001-schema.sql#L125)）。

## 改善方針

対象クエリごとに `EXPLAIN (ANALYZE, BUFFERS)` を取り、選択性・並び替えコスト・読み取り/書き込み頻度を見て複合・部分索引を選ぶ。候補は `(post_id, created_at, id)` 系、逆方向関係のキー、投稿の作者/時刻・削除状態を含む索引。ただし同じ列を機械的に追加しない。

## 制約・リスク

- 索引は書き込み・初期化時間・ディスクを増やす。投稿期間中の更新が重くなる可能性を含めて測る。
- partial indexの述語とアプリの削除・置換条件が一致していること。クエリプランナが使用可能なクエリ形を維持する。
- 初期化スクリプトと追試で再現され、再起動後も有効であること。DBのデータ同期耐久性を落とさない。

## 検証・受け入れ条件

代表的なベンチ規模・選択性で実行計画、バッファ読み込み、書き込み速度、初期化所要時間を比較する。関連する全ページング・関係APIで結果順と件数を一致させ、総スコアとFAIL有無を確認する。
