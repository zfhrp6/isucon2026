# usersテーブルロックの競合範囲を調べる

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: 登録と投稿期間開始で取得するテーブルロックの待ち・阻害範囲を切り分ける。
- 実装コミット/記録: 調査・改善記録なし。
- 検証済み/未検証: ロック箇所を確認。競合、DB待ち、得点影響は未検証。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [14 投稿期間ロック競合](14-posting-window-lock-investigation.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`registerHandler`](../webapp/go/user_api.go#L93) と[`openPostingWindowHandler`](../webapp/go/admin_posting_window_api.go#L25) は `users` に `SHARE ROW EXCLUSIVE` ロックを取得する。登録同士、期間開始、ユーザー更新の競合や、他更新を待たせる範囲を調べる。issue 14は `posting_windows` 行ロックが対象で、こちらとは別のロック機構。

## 調査・制約

- ロック取得待ち・保持時間、blocking関係、同時登録・期間開始・ユーザー更新の成功率を同時刻で観測する。
- 計測前にテーブルロックを削除・弱化しない。username UNIQUEを検討するなら既存重複確認に加え、同時登録、region変更、期間開始との整合性を検証する。

ISUCON10 `isucon10-case-035`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon10.md`）はprocess内mutexがmulti-host/multi-processの排他を共有しない例を記録している。資料はこのcloneに含まれず、本文を再確認できない場合がある。本件の競合や最適解を示すものではないが、DBロックをローカル排他で置き換える案は全アプリホストの競合者を覆うか確認する。

## 検証・受け入れ条件

競合がある/ない/未確定を根拠とともに記録する。変更する場合はユーザー重複防止・地域更新・期間開始を並行検証し、初期化と再起動後の状態も確認する。調査のみで閉じる場合も未確認事項を記す。
