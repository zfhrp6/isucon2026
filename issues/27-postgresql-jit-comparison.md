# PostgreSQL JITの単独比較をベンチマークで評価する

## 管理状況

- 状態: **調査中（代表SQL比較済み、JIT off比較設定準備済み、ベンチ効果未測）**
- 対応範囲・完了条件: JITだけを切り替えた同条件ベンチマークで、採点経路とDB負荷への影響を評価する。完了条件は条件・結果を記録し、採用または保留を判断すること。
- 実装/適用記録: リポジトリにベンチ比較候補 `jit=off` を追加。実機への適用・reload・ベンチは未実施。これまでの実測中はtransaction内の `SET LOCAL jit=off` のみ。
- 検証済み/未検証: 実機の代表SELECTをREAD ONLYで比較し、単発実行時間とJIT時間を観測。負荷ベンチ、反復・統計比較、得点・活動完了率、全体CPUへの効果は未計測。
- 期待効果: **大（仮説）**。実測効果: **局所SQL改善確認（単発比較）、得点未計測**。
- 関連issue: [01 フィード候補取得](01-feed-candidates.md)（対応済み範囲を再開しない）、[13 サーバ設定](13-server-settings.md), [23 デプロイ来歴](23-deployment-provenance.md), [24 採点経路計測](24-scoring-path-measurement.md), [26 PostgreSQL索引調査](26-postgresql-index-investigation.md)

## 採点への寄与

JITを無効にすれば、JITコンパイル時間が支配的なSELECTの応答を短縮できる可能性がある。採点経路や実ベンチの仕事量をどれだけ改善するかは未確認である。単発の代表SQL比較からスコア改善倍率や得点を予測しない。

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

## 次の改善・検証方針

### 実機での比較手順

以下はユーザーが実施する運用手順で、ここでは実機に適用していない。現在の設定、設定元、稼働中の実効値をまず記録する。PostgreSQL 16ではsuperuser接続で `SELECT name, setting, source, sourcefile, sourceline FROM pg_settings WHERE name IN ('jit','jit_above_cost','jit_inline_above_cost','jit_optimize_above_cost') ORDER BY name;` を実行する。`pg_settings` が示す値に加え、`pg_db_role_setting` のDB/role設定やアプリケーション接続後のセッション値も確認する。`conf.d/isucon.conf` の指定がそれらに上書きされている場合、今回の設定変更だけでは条件を切り替えられないため、追加の設定変更をせず比較を保留する。

1. 初回反映前に、実機と配備元で `postgresql.conf`、`pg_hba.conf`、`pg_ident.conf`、`conf.d/isucon.conf` の4ファイルを照合する。`ops/deploy-config.sh postgresql` は4ファイル全てを上書きする。意図した `jit` 行以外に差があれば、比較も反映も中止し、その差を解消してから再開する。dry-runは対象を表示するだけで、ファイル内容の照合はしない。採点環境を初期化し、全リージョンで同一のbaseline設定・アプリartifact・DB状態になるよう揃える。`ops/config/postgresql/isu-asia2/conf.d/isucon.conf` の `jit` を `on` にしてbaseline設定を作り、dry-run後に `./ops/deploy-config.sh postgresql` で反映する。`pg_settings` で `jit=on` と設定元を再確認し、全リージョンのベンチを複数回実行してbaselineを記録する。
2. 各走行の前に同一の初期化を行い、他設定・コード・データ・負荷条件を揃える。`jit` の一行だけ `off` に変え、同じdry-run確認とデプロイを行う。実効値が `off` になったことを確認して同じベンチを同数回走らせる。走行順の影響を抑えるため可能ならon/offを複数ブロックで交互に比較し、初回とwarm実行を区別する。
3. ベンチ後は `jit` を `on` に戻して再デプロイし、全閾値を含めて実効値を確認する。候補を採用しない場合は、実機を記録したbaselineへ戻したうえで、追跡対象の配備元設定からも `jit=off` 行を削除または復元する。そうしないと後日の設定配備でoffが再適用される。復元後も全リージョンの採点とFAIL有無を確認する。

比較結果には、各走行の設定/artifact/初期化識別子・時刻・負荷条件・設定元と実効値、スコアとFAIL、対象活動の成功数/完了率、API p50/p95、DB CPU・クエリ時間・読取量、接続数・pool待ち・lock待ち、他SQLの悪化、処理件数を記録する。初回とwarm実行の結果を分け、generic/custom planやcache状態の違いも記録する。実測の採否は同条件の複数走行から判断する。

得点だけでなく、対象活動の成功数/完了率、API p50/p95、DB CPU・クエリ時間・読取量、接続数/接続プール待ち/ロック待ち、他SQLの悪化、実現処理件数/負荷も比較する。設定と初期状態を揃えたbaselineを使い、スコア・処理量・正しさを同時に評価する。差が再現せず、得点や活動完了にも改善がなければ永続的なJIT無効化は保留し、元の設定を維持する。

## 制約・受け入れ条件

- リポジトリには比較候補として `jit=off` を記載した。実機への永続設定適用・reload・ベンチマークは未実施で、採否も未決定。
- 比較はJIT設定だけを切り替え、SQL結果・認可・ページング・出力形式を維持する。他のSQLや初期化時間を含む負荷も確認する。
- 初期化・初期データ/画像、別リージョンread-after-write、再起動後のデータ取得、DB同期耐久性を損なわない。変更を採用する場合は全リージョンの採点とFAIL有無を確認する。
- ベンチマーク結果が得られるまでは、単発SQLの局所改善をスコア向上実証として扱わない。
