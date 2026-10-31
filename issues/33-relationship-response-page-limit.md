# 関係一覧の51件目を応答・署名生成前に除く

## 管理状況

- 状態: **対応中（ローカル実装済み、実機検証待ち）**
- 対応範囲・完了条件: `relationship_list_api.go`で継続判定専用の行をプロフィール応答へ変換しない。
- 実装コミット/記録: `work1`の本変更。取得件数から`hasMore`を確定し、先頭50件だけを署名鍵変換・プロフィール応答生成へ渡す。SQLの`LIMIT 51`は維持する。
- 検証済み/未検証: `go test -v ./...`・`go build -o /tmp/isureal-issues-32-33 ./cmd/isureal`成功。PostgreSQL統合テストは`ISUREAL_TEST_DATABASE_DSN`未設定のためスキップ。全relationship種別の0・50・51件のHTTP比較、画像有無による署名回数・性能・得点効果、実機適用は未確認。
- 期待効果: **小（限定的な不要加工の削減）**。実測効果: **未計測**。
- 関連issue: [32 フィードの応答件数](32-feed-response-page-limit.md), [34 応答内の署名再利用](34-response-image-signature-reuse.md)

## 根拠と改善方針

[`getRelationshipsHandler`](../webapp/go/relationship_list_api.go#L79)は最大51行すべてで署名鍵の変換とプロフィール応答生成を行い、プロフィール画像があればURL署名も作成してから51件目を捨てる。取得件数で`hasMore`を決め、最大50行だけを変換する。

実装はフィードとは別handlerなのでissue 32と独立して扱う。検索・交流一覧の同種の加工は既存issue [04](04-user-search.md)・[05](05-interaction-reads.md)に記録する。

## 検証・受け入れ条件

friends・following・followers・incoming/outgoing requestsについて0・50・51件を比較する。順序、カーソル、`hasMore`、status、bio秘匿、active条件を維持し、プロフィール画像あり/なしで署名回数を確認する。SQLの51件取得は維持する。
