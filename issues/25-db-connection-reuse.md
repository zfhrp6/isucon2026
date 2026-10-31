# PostgreSQL接続のidle再利用を実測する

## 管理状況

- 状態: **調査中**
- 対応範囲・完了条件: Go database/sql poolのidle接続再利用費用をDB統計・負荷計測で確認する。
- 実装コミット/記録: 設定変更記録なし。
- 検証済み/未検証: `SetMaxOpenConns(30)`のみ設定、3アプリ合計上限90、DB max_connections 100を確認。idle閉鎖・待ち・新規接続費用・得点効果は未計測。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [13 サーバ設定](13-server-settings.md), [08 クエリ索引](08-query-indexes.md)

## 現状と仮説

Goは`SetMaxOpenConns(30)`を設定し、`SetMaxIdleConns`は指定していないためdatabase/sqlの既定idle上限2が使われる。3 appの最大open合計は90、DB `max_connections` は100。遠隔appからDBへのRTTは過去のping観測で約101msだったが、接続再作成頻度や負荷中の待ちを示すものではない。nginx upstream keepaliveとは別の接続経路。

## 調査・制約

- 負荷中の`DBStats.MaxIdleClosed`、`WaitCount`、`WaitDuration`、接続数、新規接続費用、prepared query warmupを採る。
- `MaxOpenConns`は維持し、idle候補値8/16等を比較する。候補値を最適値と決めつけない。
- 3台合計と管理接続を含むDB上限、メモリ、他の利用者を確認し、接続枯渇を避ける。

## 検証・受け入れ条件

同じ負荷・初期状態で接続再作成、pool wait、DB負荷、API p95、成功率/得点を比較し、接続上限を超えないことを確認する。効果がなければ既定値を維持する。
