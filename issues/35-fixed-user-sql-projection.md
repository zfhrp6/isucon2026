# 固定のユーザーSQL列リストの再生成を避ける

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: `user.go`の固定列リストと、そのSQL呼出箇所の文字列生成を減らす。
- 実装コミット/記録: なし。
- 検証済み/未検証: `userColumnsAs`が毎回スライス・列別`fmt.Sprintf`・Joinを行うことを確認。CPU・割当・得点への寄与は未計測。
- 期待効果: **小（固定費の候補）**。実測効果: **未計測**。
- 関連issue: [02 viewer context](02-viewer-context.md), [17 署名CPU](17-image-signature-cpu.md)

## 根拠と改善方針

[`userColumnsAs`](../webapp/go/user.go#L44)のalias・prefixは各呼出箇所で固定されているが、投稿・feed・交流・関係一覧のSQL構築時に同じ射影文字列を毎回生成する。`strings.Join(userColumns, ", ")`を繰り返す経路もある。固定部分を事前構築できるか検討する。

列順、alias、入れ子のsqlx mappingを維持し、任意引数を受ける汎用cacheへ広げない。DB往復やHMACと比較して寄与は未確認なので、上位候補とは別の小さな変更として評価する。

## 検証・受け入れ条件

変更前後のSQL射影とUser/著者/reactorへのmappingが一致する。並行呼び出しで共有値を変更しない。文字列生成のCPU/allocsと実APIの寄与を比較する。
