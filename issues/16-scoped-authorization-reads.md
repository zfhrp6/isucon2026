# フィード認可用の読み取りを要求スコープに絞る

## 管理状況

- 状態: **対応中（feat/16にフィード変更あり、master未統合。フィード受入・実測待ち）**
- 対応範囲・完了条件: フィード候補SQLへ渡さないviewer関係情報の照会を除く。全feed種別のHTTP応答・認可、地域即時反映、性能・得点効果の確認が残る。
- 実装コミット/記録: `feat/16` に `loadFeedViewer` を追加し、閲覧者と当日投稿判定だけを読む。friendsがある経路ではfeed前SELECTを5回から2回、フィードSQLを含め6回から3回へ削減。友達がいない経路では前SELECTを4回から2回へ削減。未投稿時の早期空応答は[issue 15](15-empty-feed-short-circuit.md)の既存処理を維持。master未統合。
- 検証済み/未検証: `go fmt ./...` と `go test ./...` は成功。PostgreSQL oracleテストは環境にDBがなく未実行。HTTP応答・認可境界、同条件性能・得点比較、別リージョンread-after-writeは未確認。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [01 フィード候補取得](01-feed-candidates.md), [02 閲覧者コンテキスト](02-viewer-context.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

`getFeedHandler` は以前、全feed種別で `loadViewerContext` を呼んでいた。フィードSQLはviewer IDを使ってブロック・友達・フォロー・可視性を判定し、friends-of-friends の順位も計算するため、Go側で取得した関係集合を候補SQLへ渡していなかった。未統合の `feat/16` では `loadFeedViewer` に切り替え、active userの確認と当日投稿判定を維持しつつ、ブロック一覧・友達一覧・友達の友達/相互集計をフィード経路から除いた。SQL往復・返却行・Go側集計の削減が期待できるが、応答時間・得点効果は未計測。

投稿詳細・コメント・RealMoji経路のviewerContext照会や要求内再取得は[issue 02](02-viewer-context.md)と役割を分けて扱い、効果を二重計上しない。issue 02は残る経路で必要な逐次照会をまとめる変更として評価する。画像認可は対象外。

## 調査・制約

- フィードで必要なviewer・当日投稿判定は維持し、関係認可は現行候補SQLで行う。issue 02のviewerContext照会削減とは適用経路を分けて評価する。
- 既取得値を使う場合も、ロック取得後に必要な認可再確認を省略しない。TTLやプロセスキャッシュによる代替は不可。
- `canView`、`canComment`、画像の有効状態・削除猶予は別契約として保持する。

## 検証・受け入れ条件

各feed種別のSQL回数・返却行数・時間を同条件で比較する。フィードの権限境界（友達、友達の友達、ブロック、未投稿、公開範囲、期間終了）の結果、別リージョンread-after-write、HTTP性能・スコアを確認する。フィードSQLと認可・ページング条件は変更していないが、PostgreSQL oracleの実行とHTTP統合確認は未完了のため、このissue全体は未完了とする。
