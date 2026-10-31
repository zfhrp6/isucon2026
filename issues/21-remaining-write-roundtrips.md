# 応答以外に残る書き込み経路のDB往復を測る

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: 友達申請、プロフィール更新、ログイン等の複数読書きを測り、削減候補を選別する。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: 現行経路をコードで確認。往復数、待ち、性能・得点効果は未計測。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [09 書き込み応答往復](09-write-roundtrips.md), [02 閲覧者コンテキスト](02-viewer-context.md)

## 現状と仮説

友達申請では相手を探すため関係一覧を広く取得する経路、プロフィール更新では項目ごとのUPDATE、ログインではユーザー・セッション情報を複数回読む経路がある。09で完了した投稿・コメント・RealMoji成功応答用SELECT削減とは別範囲。

## 調査・制約

### 実装箇所ごとの削減候補

| 実装箇所 | 現在の余分な取得・更新 | 対処時の契約 |
|---|---|---|
| `user_api.go:loginHandler` → `session.go:createLoginSessionTx` | 呼び出し元で利用者行をロックした後、helperでも同じ利用者をSELECTしてロックする | 同じtransactionの二度目のロックは新しい競合待ちではないが往復は増える。helper単独の呼出経路も確認し、ロック済みの前提を明示する |
| `friend_request_api.go:createFriendRequestHandler` | 相手1人との友達状態を確認するため友達一覧を全件取得。既存申請ID取得後の再取得もある | 対象の組に限定した照会とRETURNING等を評価し、逆方向申請・同時更新・競合応答を維持する |
| `user_api.go:updateMeHandler` | 項目ごとにUPDATEし、最終状態をSELECTする | 指定項目をまとめても未指定項目・更新時刻・地域反映を維持する |
| `post_api.go:updatePostHandler` | caption/visibilityの個別UPDATE、投稿期間・投稿の再取得 | 所有者・期間終了判定と最終応答、公開範囲の即時反映を維持する |

これらは別handlerなので実装・比較を一つずつ行う。ログインの認証失敗前の排他取得は[issue 18](18-users-table-lock-investigation.md)、読み取り専用の現在投稿取得は[issue 36](36-current-post-read-roundtrips.md)で扱い、往復とロック競合を同一視しない。

- API別のSQL回数・実行時間・待ち、結果行数を計測して優先対象を決める。
- 変更時はstatus/error precedence、transaction境界、lock order、更新対象の意味を維持する。
- 友達状態・プロフィール・セッション変更のread-after-writeと全リージョン即時反映を保つ。

## 検証・受け入れ条件

各候補の往復削減とp95を同条件で比較し、並行更新・失敗・認可ケースで応答契約を確認する。効果がなければ実装せず調査結果を記録する。
