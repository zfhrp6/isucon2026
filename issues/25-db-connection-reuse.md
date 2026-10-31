# PostgreSQL接続のidle再利用を実測する

## 管理状況

- 状態: **対応中（idle16を暫定採用、地域別open上限配分は未ベンチマーク）**
- 対応範囲・完了条件: Go database/sql poolのidle接続再利用を比較し、接続再作成・待ち・API・得点・DB資源への影響を評価する。16は単発比較で暫定採用した値で、最適値の確定ではない。
- 実装コミット/記録: `4273d4f` でsystemd `EnvironmentFile` が読む形式で設定を配布し、再起動後のプロセス環境値を検証。`ffb7cf2` でidle16、`6982c33` でregion別open上限、`a0c215f` で統合済み。現HEADの共通設定はidle16、Asia40 / Europe25 / America25。
- 検証済み/未検証: idle上限8・16の実機走行でスコア、PostgreSQL接続記録、プール統計、nginx集計を取得。16は8より一回の得点が高く、`MaxIdleClosed` と待ち統計も減少した。走行ごとの処理件数が異なり、初期検証等もログに含まれるため、設定単独の因果・再現性は未確認。DBメモリ影響、16の再走行、16超の候補は未計測。
- 期待効果: **中（仮説）**。実測効果: **idle16で高得点を単発観測、施策単独の寄与と再現性は未確定**。
- 関連issue: [13 サーバ設定](13-server-settings.md), [08 クエリ索引](08-query-indexes.md)

## 現状と仮説

Goの`ISUREAL_DB_MAX_OPEN_CONNS`既定値は30で、共通設定`ops/config/app.env`はAsia40 / Europe25 / America25（合計90）、idle16を指定する。現HEADはこれらの設定を環境設定として扱い、deploy時に総枠を検証する。地域別配分は統合済みだが未ベンチマーク。idle16の実測は配分変更前の走行なので、この配分の効果は含まない。遠隔appからDBへのRTTは過去のping観測で約101msだったが、接続再作成頻度や負荷中の待ちを示すものではない。nginx upstream keepaliveとは別の接続経路。

## 調査・制約

- `db_pool_config`と`db_pool_stats` JSONログを採る。統計にはregion、時刻、観測間隔、open/in-use/idle接続数、`MaxIdleClosed`・`MaxIdleTimeClosed`・`MaxLifetimeClosed`・`WaitCount`・`WaitDuration`の累積値と区間差分を含む。ログはアプリのAPIログレベル設定から独立して標準出力へ出す。計測はAPIリクエスト経路外で行う。
- 起動時の `db_pool_config` で3台すべてのidle設定を確認し、走行ごとに起動時刻と初期カウンターを分ける。別起動をまたぐ累積値・差分を混ぜない。DB再初期化だけではカウンターはリセットされない。
- 8・16両走行ともAsiaで観測最大`InUse=30`かつpool waitがあった一方、Europe/Americaの観測最大は17/14だった。これを受け、全台一律40ではなく総枠90を維持するAsia40 / Europe25 / America25の配分を比較候補として実装した。単発観測は将来のピーク余裕を保証しないため、スコア・処理件数・プール待ちに加え、CPU・I/O・DBロック待ちとの同時刻比較が必要。
- 16を同じ条件で再走行し、スコア・処理件数・成功率、プール統計とDBメモリの再現性を確認する。16超（例24）は`MaxIdleClosed`が残ることから追加候補にはなるが、保持接続のメモリ費用は未計測。idle上限を増やしても、同時open上限やDBロック待ちは直接解消しない。
- `WaitCount` / `WaitDuration` はdatabase/sql poolで接続待ちした統計であり、PostgreSQLロック待ちと同じではない。接続を長く占有するSQLロックが両方に影響する可能性はあるが、CPU・I/O・blocking session・トランザクション保持時間との時刻相関は未計測。

## 検証・受け入れ条件

比較記録は以下のとおり。nginx集計は初期検証等を含む全ログのスナップショットで、走行量は同一ではない。

| 条件 | 得点 | nginxログ | PostgreSQLログ | 起動時のidle設定 |
|---|---:|---|---|---:|
| baseline | 5912 | `20261031T044344Z-ZbSHyu` | `20261031T044349Z-u4MQEj` | 2（既定値、起動ログ未確認） |
| 8 | 10750 | `20261031T050904Z-pylZ9L` | `20261031T050909Z-fvn6zL` | 8（3台確認） |
| 16 | 11450 | `20261031T051553Z-Z9ANpD` | `20261031T051559Z-xTL8bT` | 16（3台確認） |

別走行の6217点（`20261031T045722Z-E1mI1a` / `20261031T045728Z-9Gx2DK`）は、3台の起動journalでidle設定2を実測確認したため、8の比較結果には含めない。baselineの起動ログは確認できておらず、表の2は既定値からの想定である。

idle8→16ではPostgreSQL接続記録が266→129件（各走行の管理接続1件ずつを含む）、`MaxIdleClosed`累積合計が238（Asia 195 / Europe 19 / America 24）→79（71 / 6 / 2）へ減少した。Asiaの`WaitCount`は25755→23796、`WaitDuration`は2967782→2270369 ms。Europe / Americaのpool waitは両走行とも0。観測最大`InUse`はAsiaで30→30、Europeで13→17、Americaで14→12だった。

同じ8→16のnginx全ログは8967→9562リクエスト。`POST /api/posts`成功数は360→352、friend feed成功数は539→625。Asiaのfriend feed成功応答p95は2.156→1.844秒、画像は0.633→0.445秒、投稿は3.346→3.302秒。Europe投稿p95は5.961→5.489秒、America投稿p95は3.874→4.630秒で、リージョン別の変化も一様ではない。単発のスコア上昇と接続再作成減少は16を暫定候補として支持するが、処理量の差を含むため設定単独で全体性能・得点が上がったとは断定しない。

PostgreSQLの`log_lock_waits`に続く`STATEMENT`記録では、`posting_windows`の`FOR UPDATE`が148→112件、users関連が60→56件（うち`LOCK TABLE`は10→19件）。16でも投稿期間ロック待ちは残る。pool waitはDBロック待ちを計る指標ではなく、ロック待ちが接続を占有してpool待ちを増やす可能性も未切り分け。issue [14](14-posting-window-lock-investigation.md) の投稿期間ロック調査を優先し、usersテーブルの待ちは[18](18-users-table-lock-investigation.md)と分けて扱う。

統計のみでは接続確立時間やDBメモリを直接測れない。まずidle16を同条件で再走行して再現性とDBメモリを確認する。16の優位が再現しない場合は8との比較へ戻る。2は必要に応じbaseline対照として再測定する。idle24/30は保持接続のメモリ余裕を確認してから別比較する。地域別open配分はHEADに統合済みだが未試験であり、idle調整だけでロック待ちや最大open接続到達を解消したとは扱わない。open配分の評価では、16 / 30の既存走行と新しい40 / 25 / 25走行を混同せず、設定反映を起動ログで確認する。最新idle16のPostgreSQLログには投稿期間・users関連の遅いSQLがなお記録されている（issue [14](14-posting-window-lock-investigation.md), [18](18-users-table-lock-investigation.md)）。connection pool待ちとDBロック待ちは別指標であり、遅いSQL記録だけから因果を決めない。
