# PostgreSQL接続のidle再利用を実測する

## 管理状況

- 状態: **調査中**
- 対応範囲・完了条件: Go database/sql poolのidle接続再利用費用をDB統計・負荷計測で確認する。
- 実装コミット/記録: `ISUREAL_DB_MAX_IDLE_CONNS`でidle上限を指定可能（未指定時2、0〜30を許可）。`MaxOpenConns(30)`は維持。起動時に設定と初回プール統計をJSONログへ記録し、以降は専用ticker goroutineから10秒ごとに統計・区間差分を記録する。停止時はmonitor終了後にDB接続を閉じる。
- 検証済み/未検証: 設定値の範囲検証を実装。3アプリ合計上限90、DB max_connections 100を確認。実機でのidle閉鎖・待ち・新規接続費用・得点効果は未計測。
- 期待効果: **中（仮説）**。実測効果: **未計測**。
- 関連issue: [13 サーバ設定](13-server-settings.md), [08 クエリ索引](08-query-indexes.md)

## 現状と仮説

Goは`SetMaxOpenConns(30)`を設定し、`SetMaxIdleConns`は指定していなかったためdatabase/sqlの既定idle上限2が使われていた。比較用に`ISUREAL_DB_MAX_IDLE_CONNS`を追加し、省略時は従来どおり2、許容値は0〜30とする。3アプリの最大open合計は90、DB `max_connections` は100。遠隔appからDBへのRTTは過去のping観測で約101msだったが、接続再作成頻度や負荷中の待ちを示すものではない。nginx upstream keepaliveとは別の接続経路。

## 調査・制約

- `db_pool_config`と`db_pool_stats` JSONログを採る。統計にはregion、時刻、観測間隔、open/in-use/idle接続数、`MaxIdleClosed`・`MaxIdleTimeClosed`・`MaxLifetimeClosed`・`WaitCount`・`WaitDuration`の累積値と区間差分を含む。ログはアプリのAPIログレベル設定から独立して標準出力へ出す。計測はAPIリクエスト経路外で行う。
- 同じ初期状態・負荷条件でidle上限2をbaselineとして記録し、8、16を比較する。接続の廃棄・作成数、プール待ち、API p95、成功率・得点、DB接続数・メモリを比較する。
- 起動ごとに統計カウンターの基準が作られる。比較では起動時刻を記録し、別起動をまたいだ累積値・差分を混ぜない。DB再初期化だけではカウンターはリセットされない。
- `MaxOpenConns`は維持し、3台合計と管理接続を含むDB上限、メモリ、他の利用者を確認して接続枯渇を避ける。候補値を最適値と決めつけない。

## 検証・受け入れ条件

同じ負荷・初期状態で接続再作成、pool wait、DB負荷、API p95、成功率/得点を比較し、接続上限を超えないことを確認する。統計のみでは接続確立時間を直接測れないため、必要に応じてDB接続ログ等と照合する。効果が再現しなければ既定値2を維持する。得点への効果は未計測。
