# JPEGの寸法をデコード前に判定できるか調べる

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: JPEG全デコード前の寸法上限判定がCPU/RSSを減らすか、契約を保ったまま検証する。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: 画像handlerが `jpeg.Decode` を呼ぶことを確認。CPU/RSS、502との相関、得点効果は未計測。
- 期待効果: **小（仮説）**。実測効果: **未計測**。
- 関連issue: [12 プロフィール画像502](12-profile-image-502.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`putProfileImageHandler`](../webapp/go/profile_image_api.go#L17)、[`createPostHandler`](../webapp/go/post_api.go#L49)、[`replaceRealMojiHandler`](../webapp/go/realmoji_api.go#L35) は画像を `jpeg.Decode` してから寸法を確認する。プロフィール画像・RealMojiは512x512、投稿画像は4096x4096上限。大きな画像を拒否する前に全デコードするCPU/RSS費用があるかもしれないが未計測。プロフィール画像502の原因と断定できる材料はない。

## 調査・制約

- 3経路それぞれで `jpeg.DecodeConfig` による上限超過の先行拒否を測る。許容サイズ画像は引き続き完全デコードし、破損画像を検出する。
- プロフィール画像512x512、投稿画像4096x4096の境界、壊れたJPEG、truncated input、status/error bodyを維持する。

## 検証・受け入れ条件

プロフィール画像/RealMojiの512x512、投稿画像の4096x4096について境界の前後、壊れた画像、過大画像で既存の外部応答と保存結果を確認する。CPU・最大メモリ・要求時間を測り、画像更新成功率と5xxを同条件で比較する。
