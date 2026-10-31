# PostgreSQL JITの単独比較をベンチマークで評価する

## 管理状況

- 状態: **調査中（ベンチ改善報告あり、比較条件とJIT単独の寄与・採否は未確定）**
- 対応範囲・完了条件: JITだけを切り替えた同条件ベンチマークで、採点経路とDB負荷への影響を評価する。完了条件は条件・結果を記録し、採用または保留を判断すること。
- 実装/適用記録: リポジトリにベンチ比較候補 `jit=off` を追加。ユーザーからベンチ実施の報告があるが、実機の適用・reloadと実効JIT設定をログでは独立確認できていない。先行する代表SQL比較ではtransaction内の `SET LOCAL jit=off` のみを使用した。
- 検証済み/未検証: 実機の代表SELECTをREAD ONLYで比較し単発実行時間とJIT時間を観測。ユーザーから約5000点から約7500点へ改善したとのベンチ報告があるが、条件、反復数、FAIL有無、配備内容を独立確認できていない。JIT単独の寄与、活動完了率、全体CPUへの効果は未確定。
- 期待効果: **大（仮説）**。実測効果: **局所SQL改善を単発観測。スコア改善はユーザー報告（約5000→約7500、比較条件未確認）**。
- 関連issue: [01 フィード候補取得](01-feed-candidates.md)（対応済み範囲を再開しない）、[13 サーバ設定](13-server-settings.md), [14 投稿期間ロック競合](14-posting-window-lock-investigation.md), [18 usersテーブルロック](18-users-table-lock-investigation.md), [23 デプロイ来歴](23-deployment-provenance.md), [24 採点経路計測](24-scoring-path-measurement.md), [26 PostgreSQL索引調査](26-postgresql-index-investigation.md)

## 採点への寄与

JITを無効にすると代表SQLの単発実行時間が短くなり、ユーザー報告ではスコアも約5000点から約7500点へ上がった。報告された差は改善の手掛かりだが、走行条件とJIT以外の差を確認できておらず、約50%をJIT単独の効果とは判断しない。

## 比較条件

2026-10-31にisu-asia2上のPostgreSQL 16.15で、稼働中の実DBを読み取り専用で比較した。JITは有効、閾値は `jit_above_cost=100000`、`jit_inline_above_cost=500000`、`jit_optimize_above_cost=500000`。実測は全て `READ ONLY`、`statement_timeout=20s`、`lock_timeout=1s`、ベンチ負荷なし、DBはidle状態の接続4本だった。

`posts` は推定625,279行、未終了投稿期間は3件。代表利用者は欧州、友達5人、follow 2人で、識別子は記録しない。公開フィードとfollowingフィードのSELECTを `PREPARE(varchar,timestamptz,varchar)` し、同じ引数 `EXECUTE(user,NULL,'')` でJIT on/offを各単発比較した。JIT無効化は比較transaction内の `SET LOCAL jit=off` のみ。比較後は `ROLLBACK` し接続を切り、新規接続で `jit=on` と各閾値が既定状態のままであることを確認した。DML/DDLと `ANALYZE` による統計更新は行っていない。

| 代表クエリ | JIT | Planning (ms) | Execution (ms) | JIT時間 (ms) | rows | shared read blocks |
|---|---:|---:|---:|---:|---:|---:|
| public | on | 1.161 | 537.673 | 519.735 | 2 | 0 |
| public | off | 1.076 | 2.927 | — | 2 | 0 |
| following | on | 1.535 | 675.227 | 593.932 | 0 | 411 |
| following | off | 1.472 | 61.856 | — | 0 | 0 |

この単発比較ではJIT無効時のSQL executionが短かった。ただし、代表利用者1人・単発・ベンチ負荷なしで、followingの結果は0行。on/offの実行順によるcache warm差があり、特にfollowingのshared read blocksがon 411/off 0と異なる。観測差の全てをJITだけに帰属できず、他の利用者・実行計画・反復時の効果を示すものでもない。

別の負荷時ログにある遅いExecute（public 30件平均9.010秒、following 20件平均11.172秒）は、今回の単発比較と異なる負荷・母集団である。直接の前後比較やJIT因果の根拠には使わない。実機のEXPLAIN JSONとsummaryはGit管理外の `logs/postgresql/20261031T040646Z-oUxB3N/readonly-plans-orqj4j6j/` に保存されており、リポジトリには同梱しない。

## 報告されたベンチと付随ログ

ユーザー報告のベンチスコアは約5000点から約7500点に改善した。走行回数、初期状態、比較順、FAIL有無、アプリartifact、実効JIT設定をこの報告から独立確認できないため、因果効果と再現性は未確定とする。

付随するPostgreSQLログ（2026-10-31 04:30:37–04:33:33 UTC）と3台のnginxログ（04:30:38–04:33:01 UTC）は初期検証や異常系を含むファイル全体で、ベンチ負荷の時間帯だけに分離できていない。nginxのfriends feed status 200はアジア446件（平均0.344秒、p95 0.617秒）、米州32件（平均1.437秒、p95 2.658秒）、欧州27件（平均1.603秒、p95 2.016秒）。29 byte応答があっても本文を保存していないため空feedとは断定しない。これらは負荷・artifact・初期状態を揃えた前回値との比較ではなく、JIT効果の倍率として扱わない。

PostgreSQLの100ms以上のduration記録では、`posting_windows` の `FOR UPDATE` が177回（平均1.297秒、最大4.009秒）、`users` 行の `FOR NO KEY UPDATE` が198回（平均0.781秒、最大4.735秒）、`LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` が33回（平均1.687秒、最大5.779秒）だった。1秒を超えたロック待ちは「still waiting」と「acquired」が各172件記録されている。これは同じ待機の開始と取得を示す対の記録なので、344件の独立した待ちとして数えない。duration記録も遅いSQLの標本で、全実行の平均やCPU時間ではない。待ちの観測は[issue 14](14-posting-window-lock-investigation.md)と[issue 18](18-users-table-lock-investigation.md)の調査対象に関係するが、スコア改善の原因と結び付ける証拠ではない。

この取得の `settings.tsv` はJIT値を含まず、PostgreSQLログにもreload記録が見当たらないため、ログから実効JIT設定は独立確認できない。プロフィール画像PUTの502がアジアで4件、通知のupstream timeoutがアジア255件・米州2件・欧州5件あった。これらもFAILや採点対象の取りこぼしを示すものとは限らない。元ログは指定された `logs/postgresql/20261031T043333Z-IPdvxA/` と `logs/nginx/20261031T043326Z-4OWG4L/` にあり、Gitには含めない。

## 次の改善・検証方針

### 実機での比較手順

以下は条件を揃えて再比較するための運用手順。今回の記録更新では、この手順の実機適用・ベンチ実行は行っていない。現在の設定、設定元、稼働中の実効値をまず記録する。PostgreSQL 16ではsuperuser接続で `SELECT name, setting, source, sourcefile, sourceline FROM pg_settings WHERE name IN ('jit','jit_above_cost','jit_inline_above_cost','jit_optimize_above_cost') ORDER BY name;` を実行する。`pg_settings` が示す値に加え、`pg_db_role_setting` のDB/role設定やアプリケーション接続後のセッション値も確認する。`conf.d/isucon.conf` の指定がそれらに上書きされている場合、今回の設定変更だけでは条件を切り替えられないため、追加の設定変更をせず比較を保留する。

1. 初回反映前に、実機と配備元で `postgresql.conf`、`pg_hba.conf`、`pg_ident.conf`、`conf.d/isucon.conf` の4ファイルを照合する。`ops/deploy-config.sh postgresql` は4ファイル全てを上書きする。意図した `jit` 行以外に差があれば、比較も反映も中止し、その差を解消してから再開する。dry-runは対象を表示するだけで、ファイル内容の照合はしない。採点環境を初期化し、全リージョンで同一のbaseline設定・アプリartifact・DB状態になるよう揃える。`ops/config/postgresql/isu-asia2/conf.d/isucon.conf` の `jit` を `on` にしてbaseline設定を作り、dry-run後に `./ops/deploy-config.sh postgresql` で反映する。`pg_settings` で `jit=on` と設定元を再確認し、全リージョンのベンチを複数回実行してbaselineを記録する。
2. 各走行の前に同一の初期化を行い、他設定・コード・データ・負荷条件を揃える。`jit` の一行だけ `off` に変え、同じdry-run確認とデプロイを行う。実効値が `off` になったことを確認して同じベンチを同数回走らせる。走行順の影響を抑えるため可能ならon/offを複数ブロックで交互に比較し、初回とwarm実行を区別する。
3. ベンチ後は `jit` を `on` に戻して再デプロイし、全閾値を含めて実効値を確認する。候補を採用しない場合は、実機を記録したbaselineへ戻したうえで、追跡対象の配備元設定からも `jit=off` 行を削除または復元する。そうしないと後日の設定配備でoffが再適用される。復元後も全リージョンの採点とFAIL有無を確認する。

比較結果には、各走行の設定/artifact/初期化識別子・時刻・負荷条件・設定元と実効値、スコアとFAIL、対象活動の成功数/完了率、API p50/p95、DB CPU・クエリ時間・読取量、接続数・pool待ち・lock待ち、他SQLの悪化、処理件数を記録する。初回とwarm実行の結果を分け、generic/custom planやcache状態の違いも記録する。実測の採否は同条件の複数走行から判断する。

得点だけでなく、対象活動の成功数/完了率、API p50/p95、DB CPU・クエリ時間・読取量、接続数/接続プール待ち/ロック待ち、他SQLの悪化、実現処理件数/負荷も比較する。設定と初期状態を揃えたbaselineを使い、スコア・処理量・正しさを同時に評価する。差が再現せず、得点や活動完了にも改善がなければ永続的なJIT無効化は保留し、元の設定を維持する。

## 制約・受け入れ条件

- リポジトリには比較候補として `jit=off` を記載した。ベンチスコアの改善報告はあるが条件と実効JITを独立確認できておらず、採否は未決定。
- 比較はJIT設定だけを切り替え、SQL結果・認可・ページング・出力形式を維持する。他のSQLや初期化時間を含む負荷も確認する。
- 初期化・初期データ/画像、別リージョンread-after-write、再起動後のデータ取得、DB同期耐久性を損なわない。変更を採用する場合は全リージョンの採点とFAIL有無を確認する。
- 比較条件と実効JITの確認が済むまでは、今回のスコア報告をJIT単独のスコア改善実証として扱わない。
