# フィード認可用の読み取りを要求スコープに絞る

## 管理状況

- 状態: **対応中（feedの不要なviewer relationship readsを除く案が未統合）**
- 対応範囲・完了条件: feed経路で候補SQLへ渡さないviewer関係情報の照会を除く。全feed種別のHTTP応答・認可と、性能・得点効果の確認が残る。
- 実装コミット/記録: `feat/16` の `4844488`。friendsがある場合にfeed前のSELECTを5回から2回へ減らす案。友達がいない経路では4回から2回。master未統合。
- 検証済み/未検証: コード差分を確認。未投稿時の早期空応答は現masterに既に存在する（[issue 15](15-empty-feed-short-circuit.md)）。全feed種別のHTTP試験、認可境界、同条件性能・得点比較は未確認。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [01 フィード候補取得](01-feed-candidates.md), [02 閲覧者コンテキスト](02-viewer-context.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

現masterの `getFeedHandler` は全feed種別で `loadViewerContext` を呼ぶ。未統合の `feat/16` (`4844488`) はfeed候補SQLがviewer IDだけを使う点に基づき、feed前に取得する友達・友達の友達・ブロック情報を除く。friendsありでは事前SELECTを5回から2回、友達なしでは4回から2回にする。性能・得点効果は未計測。投稿詳細・コメント・RealMoji経路の再取得は別のviewerContext照会削減として[issue 02](02-viewer-context.md)で扱い、効果を二重計上しない。画像認可は対象外。

## 調査・制約

- issue 02は残るviewerContext照会をまとめる変更であり、本issueのfeed前不要照会削減と適用経路を分けて評価する。
- 既取得値を使う場合も、ロック取得後に必要な認可再確認を省略しない。TTLやプロセスキャッシュによる代替は不可。
- `canView`、`canComment`、画像の有効状態・削除猶予は別契約として保持する。

## 検証・受け入れ条件

各feed種別のSQL回数・返却行数・時間を測り、権限境界（友達、友達の友達、ブロック、未投稿、公開範囲、期間終了）と別リージョンread-after-writeの結果が一致することを確認する。
