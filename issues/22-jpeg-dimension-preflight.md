# JPEGの寸法をデコード前に判定できるか調べる

## 管理状況

- 状態: **調査中（asia1 profileでcreate-post JPEG decodeのCPU寄与を確認）**
- 対応範囲・完了条件: JPEG全デコード前の寸法上限判定がCPU/RSSを減らすか、契約を保ったまま検証する。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: 画像handlerが `jpeg.Decode` を呼ぶことと、asia1 CPU profileでCPU寄与があることを確認。RSS、寸法ごとの費用、502との相関、得点効果は未計測。
- 期待効果: **小（仮説）**。実測効果: **未計測**。
- 関連issue: [12 プロフィール画像502](12-profile-image-502.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`putProfileImageHandler`](../webapp/go/profile_image_api.go#L17)、[`createPostHandler`](../webapp/go/post_api.go#L49)、[`replaceRealMojiHandler`](../webapp/go/realmoji_api.go#L35) は画像を `jpeg.Decode` してから寸法を確認する。プロフィール画像・RealMojiは512x512、投稿画像は4096x4096上限。asia1 CPU profileの活動4区間ではJPEG decodeが18.93秒（Go CPU samplesの17.56%）で、そのうち18.86秒が `createPostHandler` からだった。decode件数、寸法、画像サイズはprofileにないため、画像1件あたりのCPUや先行判定による節約量は分からない。profile-image PUTとRealMoji置換のdecode stackは合わせて約0.07秒で、この計測をプロフィール画像502の原因とは結び付けない。

## 調査・制約

- 3経路それぞれで `jpeg.DecodeConfig` による上限超過の先行拒否を測る。許容サイズ画像は引き続き完全decodeし、破損/truncated画像の拒否を保つ。プロフィール画像/RealMoji 512x512、投稿4096x4096の境界、status/bodyを確認し、入力寸法別にCPU・memoryを測る。

## 検証・受け入れ条件

プロフィール画像/RealMojiの512x512、投稿画像の4096x4096について境界の前後、壊れた画像、過大画像で既存の外部応答と保存結果を確認する。CPU・最大メモリ・要求時間を測り、画像更新成功率と5xxを同条件で比較する。投稿成功・期限内検証も追い、profileの17.56%をそのまま削減可能量とみなさない。
