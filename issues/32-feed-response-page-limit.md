# フィードの51件目を応答・署名生成前に除く

## 管理状況

- 状態: **対応中（ローカル実装済み、実機検証待ち）**
- 対応範囲・完了条件: `feed_api.go`の応答構築で、ページ継続判定専用の51件目を加工しない。
- 実装コミット/記録: `work1`の本変更。取得件数から`hasMore`を確定し、先頭50件だけを`newPostResponse`へ渡す。SQLの`LIMIT 51`は維持する。
- 検証済み/未検証: `go test -v ./...`・`go build -o /tmp/isureal-issues-32-33 ./cmd/isureal`成功。PostgreSQL統合テストは`ISUREAL_TEST_DATABASE_DSN`未設定のためスキップ。全feed種別の0・50・51件のHTTP比較、署名呼出数・API時間・得点効果、実機適用は未確認。
- 期待効果: **小（限定的な不要加工の削減）**。実測効果: **未計測**。
- 関連issue: [01 フィード候補取得](01-feed-candidates.md), [17 署名CPU](17-image-signature-cpu.md), [34 応答内の署名再利用](34-response-image-signature-reuse.md)

## 根拠と改善方針

[`getFeedHandler`](../webapp/go/feed_api.go#L65)はSQLが返す最大51投稿すべての画像URL・プロフィール画像URLを生成し、その後で`hasMore`を判定して末尾を捨てる。画像ごとの1000段署名計算は残るため、オブジェクト再利用後も返さない投稿の加工は余計になる。

取得件数から`hasMore`を確定し、応答対象を最大50投稿にしてから応答を構築する。SQLの`LIMIT 51`は継続判定に必要なので維持する。SQL候補取得を改善した対応済みissue 01とは別の実装箇所である。

## 検証・受け入れ条件

0・50・51投稿、全feed種別、ランキングscore・順序・カーソル・`hasMore`・認可を比較する。返さない51件目のURL生成がなくなり、返す画像URLは同じ署名契約を満たすことを確認する。署名呼出数とAPI時間を測り、少量の削減を全体律速の解消とみなさない。
