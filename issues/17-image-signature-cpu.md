# 画像署名計算のCPU・割り当てを測る

## 管理状況

- 状態: **対応中（HMAC object/buffer再利用は現HEAD統合済み、同条件のAPI・得点比較は未確認）**
- 対応範囲・完了条件: 署名生成のHMAC反復が頻出応答のCPU・割り当てに与える寄与を測定する。
- 実装コミット/記録: `69d6365`でHMAC stateと出力bufferの再利用を実装し、現HEADに統合済み。再測定で実機適用を確認。同条件比較の結果は別途確認する。
- 検証済み/未検証: 初回・追加走行のCPUと追加走行のallocsを確認。署名CPUは初回68.18/107.78秒、追加走行38.99/73.42秒で、条件を揃えた比較ではない。API要求数あたり署名数、再利用率、得点改善幅は未計測。
- 期待効果: **中（仮説）**。実測効果: **HMAC再初期化のCPU寄与減少を別走行で観測。スコア因果・キャッシュの効果は未計測**。
- 関連issue: [06 画像読み出し](06-image-read.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

保存profileの旧実装は署名ごとに1000回`hmac.New`を呼び出していた。現HEADの[`imageSignature`](../webapp/go/image_signature.go#L25)は一度生成したHMACを`Reset`し、bufferを再利用して1000段の出力を維持する。以下の数値は旧実装の観測で、現在のCPU比率や節約率ではない。

2026-10-31にasia1で取得したCPU profileの活動4区間（各30秒、合計120.017秒）では、Go CPU samplesの107.78秒中68.18秒（63.26%）が署名関数以下のstackに属した。画像URL生成側の`signedImageURL` inclusive CPUは63.55秒で、そのうち63.43秒が署名生成。別経路の画像取得側`getImageHandler`では署名検証が4.75秒だった。内訳は親子stackが重なるため加算しない。`crypto/hmac.New`は31.52秒（CPU samplesの29.24%）だった。

呼出元stackでは、月・日単位のMemories一覧handlerが署名35.19秒、export handlerが19.85秒、feed handlerが5.47秒だった。`newPostResponse` 等の共通helperを別routeのhandlerに加算しない。profileにAPI request数や画像URL数は含まれず、各API要求あたりの費用、採点活動の成功率、得点寄与との因果は未確認。

## HMAC再利用後の再測定

別走行の活動4区間（CPU profile 2〜5、約120秒）ではGo CPU samples 73.42秒中、署名計算38.99秒（53.11%）、`crypto/hmac.New` 0.07秒だった。`pass: true`、score 11953はユーザー報告である。初回の署名68.18秒、`hmac.New` 31.52秒から減少を観測したが、採点フェーズ・処理件数を揃えた比較ではなく、差をそのまま高速化率や得点改善幅としない。[issue 30](30-asia1-cpu-profile.md)に採取条件を記録する。

残る署名計算回数の削減は[issue 34](34-response-image-signature-reuse.md)で、リクエスト内の重複排除と上限付き計算結果キャッシュを扱う。HMACの1000段の出力と画像認可・期限の検証を保つ。

## 調査・制約

- [asia1 CPU profile記録](30-asia1-cpu-profile.md)の旧実装に対し、現HEADの再利用実装を同条件で比較する。再生成する旧コードは比較テスト内にもあるが、本番経路ではない。テストコードの存在、互換性確認、実負荷での効果測定は別の確認事項として記録する。
- CPU profileから割り当てbyteは分からない。実装前後にallocs/CPU、API時間、画像URL数を同一条件で測る。request内重複署名は別の[issue 34](34-response-image-signature-reuse.md)、返さない行の署名生成は[issue 32](32-feed-response-page-limit.md)・[33](33-relationship-response-page-limit.md)・[04](04-user-search.md)・[05](05-interaction-reads.md)で扱う。
- 署名検証、有効期限、署名鍵の扱い、画像の最新認可・有効状態確認を変更しない。

## 検証・受け入れ条件

既存署名とバイト単位で同じ出力を確認し、期限・改ざん・異なる画像IDのケースを検証する。feed、Memories月/日、export、画像取得を分け、CPU/allocs・API時間・活動完了数・scoreを同一条件で比較する。署名鍵、寿命、画像認可・削除反映を維持する。
