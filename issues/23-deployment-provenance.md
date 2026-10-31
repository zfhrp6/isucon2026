# ベンチ記録と配備ソース・設定の来歴を結び付ける

## 管理状況

- 状態: **未着手**
- 対応範囲・完了条件: ベンチ時のソースツリー・生成物・設定・DB変更状態を再現可能に記録する方法を調べる。
- 実装コミット/記録: 実装記録なし。
- 検証済み/未検証: 現行deploy/config/SQL適用の責務を確認。過去走行の配備対応付けは未確認。
- 期待効果: **中（再現性仮説）**。実測効果: **未計測**。
- 関連issue: [13 サーバ設定](13-server-settings.md), [10 永続性・再起動](10-durability.md)

## 現状と仮説

アプリのdeployはビルド・バイナリ配置を行う一方、SQL schema/index適用は別手順である。設定配備も別で、commit IDだけではdirty working tree由来のソースを識別できない。ベンチ結果と実際のコード・DB状態が対応付いていないと、比較結果の再現性を損なう。

## 調査・制約

- source tree digest、アーティファクトhash、設定digest、schema/index適用状態、ベンチ条件をどう記録するか検討する。
- [性能改善候補の計測ガイド](measurement-guide.md)に従い、run_id、地域・件数・得点、初期化/検証/負荷/終了のUTC半開区間、統計snapshot世代も対応付ける。時刻差の比較にはmonotonic経過時間とホスト間clock offsetを併記する。
- dirty treeも識別できるようにし、秘密設定値は記録対象にしない。
- deploy.shがSQLを適用したと誤認させない。競技DBへのschema変更やdeploy方式拡張は本issueだけで承認された範囲ではない。

ISUCON14 `R14-07`（別リポジトリ `isucon_agent_skills` の `research/analysis/cases/isucon14.md`）では、変更がアプリに反映されず性能評価が誤解を招いた著者報告がある。資料はこのcloneに含まれず、本文を再確認できない場合がある。本リポジトリのdeploy不具合を示す証拠ではないが、ベンチ前に稼働中artifactと変更内容が一致することを確認する根拠になる。

## 検証・受け入れ条件

同じclean/dirty treeとアーティファクトが識別でき、別ソース・設定・schema状態は区別されることをfixtureで確認する。ベンチ記録から配備物を特定できるかレビューする。
