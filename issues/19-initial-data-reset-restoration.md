# 初期データ変更後の初期化復元範囲を確認する

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: 初期状態に含まれる投稿・コメント・画像等を変更した後、初期化が元の状態へ戻すか検証する。
- 実装コミット/記録: 再現・修正記録なし。
- 検証済み/未検証: 初期化SQLとsnapshot対象を確認。実データでの欠落再現は未実施。
- 期待効果: **大（失敗防止の仮説）**。実測効果: **未計測**。
- 関連issue: [10 永続性・再起動](10-durability.md), [06 画像読み出し](06-image-read.md)

## 現状と仮説

`reset.sql` は初期化境界後の行を除去し、users・relationsをsnapshotから復元する。一方、`003-initial-snapshot.sql` はusers・relationsのsnapshotで、posts/comments/realmojis/imagesを含まない。初期postのcaption・visibility・deleted状態、初期commentのdeleted状態、初期RealMojiのreplaced/deleted状態を変更しても、元状態へ戻らない可能性がある。commentのbody更新APIは確認できていないため、body変更は仮説に含めない。初期プロフィール画像を置換・削除すると、`profile_image_api.go` が旧画像blobを物理削除し、初期userの画像IDだけ復元されてblobが戻らない可能性もある。対象初期行が実際に存在し、変更可能かは未確認。

## 調査・制約

- 隔離fixtureで初期ユーザー画像の更新・削除後にinitを実行し、参照先とblobの両方を確認する。
- 初期post/commentの変更・削除後にも同様に確認する。実際の初期データを保護し、競技DBで試さない。
- issue 10は全変更共通の受け入れ条件。本issueは具体的な復元漏れの再現と復元方法の検討を担当する。

ISUCON13 `isucon13-agatan-fail-atomicity-cache`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon13.md`）はtransaction分割後の部分状態をcacheへ公開しない教訓を記録している。資料はこのcloneに含まれず、本文を再確認できない場合がある。本件の初期化漏れを示す証拠ではないが、復元設計でも部分状態の可視化とcommit境界を確認する。

## 検証・受け入れ条件

初期状態の行・画像を含むfixtureで変更→初期化を繰り返し、元状態と一致することを検証する。問題が再現しなければ対象データと根拠を記録して調査を閉じる。
