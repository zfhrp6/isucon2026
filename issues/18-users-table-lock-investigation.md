# usersテーブル/行ロックの競合範囲を調べる

## 管理状況

- 状態: **調査中（users行・テーブルロックの遅いSQLを観測、阻害関係と採点寄与は未確認）**
- 対応範囲・完了条件: 登録・投稿期間開始時のテーブルロックと、API transaction内のusers行ロックの待ち・阻害範囲を切り分ける。
- 実装コミット/記録: 改善実装なし。2026-10-31 05:13:32–05:16:00 UTCのPostgreSQLログを追加確認。
- 検証済み/未検証: 100ms以上として記録されたusers行ロックは254件（平均0.584秒、最大3.492秒）、`LOCK TABLE users` は42件（平均1.332秒、最大5.768秒）。これらのdurationに待ち時間が含まれる可能性はあるが、個別blocker、競合API、採点対象の取りこぼしとの因果は未確認。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [14 投稿期間ロック競合](14-posting-window-lock-investigation.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

[`registerHandler`](../webapp/go/user_api.go#L93) と[`openPostingWindowHandler`](../webapp/go/admin_posting_window_api.go#L25) は `users` に `SHARE ROW EXCLUSIVE` ロックを取得する。加えて複数のAPI transactionがusers行を`FOR NO KEY UPDATE`する。関係変更ヘルパー[`beginRelationshipTx`](../webapp/go/relationship.go#L30)は主体と対象の行を順にロックする。コメント作成も両ユーザーをロックして認可SQLを実行するが、この経路では画像を書き込まない（[`comment_api.go`](../webapp/go/comment_api.go#L31)）。RealMoji作成は[`beginRelationshipTxWithActor`](../webapp/go/relationship.go#L51)を通じて両ユーザーをロックし、認可SQLと画像書き込みを行ってからcommitする（[`realmoji_api.go`](../webapp/go/realmoji_api.go#L35)）。ログインも秘密鍵を検証する前にusername該当行をロックする（[`user_api.go`](../webapp/go/user_api.go#L136)）。登録/期間開始のテーブルロックと、これらの行ロックは別経路なので、競合関係を分けて調べる。issue 14は`posting_windows`行ロックが対象。

投稿作成は先にusers行をロックし、その後`posting_windows`行を`FOR UPDATE`する（[`post_api.go`](../webapp/go/post_api.go#L96)、[`post_api.go`](../webapp/go/post_api.go#L107)）。そのため投稿期間ロックを待つtransactionがusers行ロックを保持し、同じ利用者の別操作へ待ちを波及させる連鎖が考えられる。これはコードから分かるロック順序に基づく仮説で、保存ログから個別blockerやこの連鎖が実際に起きたとは確認できていない。

同じPostgreSQLスナップショットでは、`FOR NO KEY UPDATE` を含むusers行ロック記録が254件（平均0.584秒、最大3.492秒）、`LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` が42件（平均1.332秒、最大5.768秒）あった。ログの全期間・SQL duration集計であり、全実行の平均ではない。行ロックは登録・期間開始のテーブルロックとは別の経路で、ユーザー単位の操作を含み得る。ログ上の遅さだけから、それぞれの待ち時間・blocking関係や得点影響は特定できない。保存ログはGit管理外の `logs/postgresql/20261031T051559Z-xTL8bT/isu-asia2/postgresql.log`。初期検証等を含み得て、走行中のartifact・採点区間との対応は未確認。

## 調査・制約

- 遅いSQL記録を特定の競合と同一視せず、ロック取得待ち・保持時間、blocking関係、同時登録・期間開始・ユーザー行をロックするAPIの成功率を同時刻で観測する。ログ上の `still waiting` と `acquired` は同一待機の対として数える。
- 計測前にテーブルロックを削除・弱化しない。username UNIQUEを検討するなら既存重複確認に加え、同時登録、region変更、期間開始との整合性を検証する。

ISUCON10 `isucon10-case-035`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon10.md`）はprocess内mutexがmulti-host/multi-processの排他を共有しない例を記録している。資料はこのcloneに含まれず、本文を再確認できない場合がある。本件の競合や最適解を示すものではないが、DBロックをローカル排他で置き換える案は全アプリホストの競合者を覆うか確認する。

## 検証・受け入れ条件

競合がある/ない/未確定を根拠とともに記録する。変更する場合はユーザー重複防止・地域更新・期間開始を並行検証し、初期化と再起動後の状態も確認する。調査のみで閉じる場合も未確認事項を記す。
