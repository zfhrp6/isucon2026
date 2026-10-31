# 画像署名計算のCPU・割り当てを測る

## 管理状況

- 状態: **調査中（asia1のCPU profileで高いCPU寄与を確認、仕様を保つ最適化と得点効果は未検証）**
- 対応範囲・完了条件: 署名生成のHMAC反復が頻出応答のCPU・割り当てに与える寄与を測定する。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: 署名ループ構造とasia1の120秒CPU profileを確認。活動4区間のCPU samplesで `imageSignature` stackが68.18秒（63.26%）、`signedImageURL` が63.55秒（58.96%）だった。allocs bytes、API要求数あたり署名数、得点効果は未計測。
- 期待効果: **中（仮説）**。実測効果: **署名処理が対象区間のGo CPU samplesの63.26%を占めることを確認。スコア寄与・最適化の効果は未計測**。
- 関連issue: [06 画像読み出し](06-image-read.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`imageSignature`](../webapp/go/image_signature.go#L24) は署名ごとに1000回 `hmac.New` を呼び出す。2026-10-31にasia1で取得したCPU profileの活動4区間（各30秒、合計120.017秒）では、Go CPU samplesの107.78秒中68.18秒（63.26%）がこの関数以下のstackに属した。画像URL生成側の `signedImageURL` inclusive CPUは63.55秒で、そのうち63.43秒が署名生成。別経路の画像取得側 `getImageHandler` では署名検証が4.75秒だった。内訳は親子stackが重なるため加算しない。`crypto/hmac.New` は31.52秒（CPU samplesの29.24%）だった。

呼出元stackでは、月・日単位のMemories一覧handlerが署名35.19秒、export handlerが19.85秒、feed handlerが5.47秒だった。`newPostResponse` 等の共通helperを別routeのhandlerに加算しない。profileにAPI request数や画像URL数は含まれず、各API要求あたりの費用、採点活動の成功率、得点寄与との因果は未確認。

## 調査・制約

- [asia1 CPU profile記録](30-asia1-cpu-profile.md)で高い署名stack寄与を確認した。まず反復回数、出力、期限を保つHMAC Reset・buffer再利用を隔離benchmarkで比較する。
- CPU profileから割り当てbyteは分からない。実装前後にallocs/CPU、API時間、画像URL数を同一条件で測る。同一URL再利用とrequest内重複署名は、実際の重複頻度を測ってから別候補として評価する。
- 署名検証、有効期限、署名鍵の扱い、画像の最新認可・有効状態確認を変更しない。

## 検証・受け入れ条件

既存署名とバイト単位で同じ出力を確認し、期限・改ざん・異なる画像IDのケースを検証する。feed、Memories月/日、export、画像取得を分け、CPU/allocs・API時間・活動完了数・scoreを同一条件で比較する。署名鍵、寿命、画像認可・削除反映を維持する。
