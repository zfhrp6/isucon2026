# PostgreSQL索引候補を実DB統計と計画で調査する

## 管理状況

- 状態: **調査中（複数の索引候補と代表計画を確認、負荷時の寄与は未判定）**
- 対応範囲・完了条件: 現HEADのスキーマと頻出クエリから索引候補を列挙し、実DBの索引・統計・代表実行計画・HTTP採点経路への影響を調べる。採用/保留/既存索引で十分/未判定を根拠付きで記録する。
- 実装コミット/記録: 画像逆引き索引は `102b7a0` で現HEADに統合済み。認証・username等の候補は未実装。
- 検証済み/未検証: 現HEADのDDLと保存済み代表実行計画を確認。負荷時の全実行計画・統計および各索引候補の同条件比較は未検証。
- 期待効果: **大（仮説）**。実測効果: **未計測**。
- 関連issue: [03 セッション照会](03-session-lookup.md), [06 画像読み出し](06-image-read.md), [08 クエリ索引](08-query-indexes.md), [13 サーバ設定](13-server-settings.md)

## 採点への寄与

索引は認証・ユーザー照会・画像有効性確認などのDBアクセス時間を減らし、採点経路の完了率に寄与する可能性がある。一方で、スキーマに索引が見当たらないことだけでは、実行頻度・行数・選択性・既存実DB索引・クエリプランが原因や律速だとは分からない。効果は候補であり、追加と性能改善を同一視しない。

## 現HEADで確認した候補

現HEADの[`001-schema.sql`](../webapp/sql/001-schema.sql)では、`users.username`、`login_sessions.token_hash`に対応する明示的な索引定義が見当たらない。画像逆引き用の`posts.image_id`、`realmojis.image_id`部分索引は定義済み。各経路は次の通り。

- `users.username`: 新規登録時の既存名確認とログインの利用者検索（[`user_api.go`](../webapp/go/user_api.go#L97)、[`user_api.go`](../webapp/go/user_api.go#L136)）。この照会候補は通常の非一意索引として評価する。索引候補の調査を、username一意性の新規制約導入と混同しない。
- `login_sessions.token_hash`: 認証ミドルウェアが `token_hash` とactive状態を条件に照会する（[`session.go`](../webapp/go/session.go#L51)）。通常索引とactive部分索引のどちらが有効かは計画・統計で比較する。
- `posts.image_id`: 画像有効性判定が投稿画像を逆引きし、所有者と削除状態を確認する（[`image_api.go`](../webapp/go/image_api.go#L76)）。現HEADでは`posts_active_image_id_author_id`部分索引が定義済み。
- `realmojis.image_id`: 画像有効性判定が有効なRealMoji画像を逆引きする（[`image_api.go`](../webapp/go/image_api.go#L80)）。現HEADでは有効行向け部分索引`realmojis_active_image_id_reactor_id`が定義済み。

これは現HEADのDDLの記述であり、実DBに同等・複合・部分索引がないことを意味しない。実機カタログの過去読取は[issue 13](13-server-settings.md)に時点付きで記録されている。実DBの現在状態は別途採取し、過去スナップショットと混ぜない。

画像IDからの逆引き索引は[issue 06](06-image-read.md)の実装として `102b7a0` で現HEADに統合済み。保存済みfollowing feed計画では`posts_active_image_id_author_id`が選択されているが、単発の代表利用者・0件結果等に限定された計画であり、全負荷での寄与は未判定。保存計画は `logs/postgresql/20261031T040646Z-oUxB3N/readonly-plans-orqj4j6j/`（Git管理外）にある。実DBの現在カタログは今回再採取していないため、DDLと稼働DBの同一性は別途確認する。認証索引候補はissue 03、ページング/一覧クエリの複合索引はissue 08に集約する。

## 調査手順

### 1. 実DBの索引カタログを採取する

読み取り専用でテーブルごとの索引定義・valid/ready状態・partial述語を確認する。次の例は実行環境のスキーマ名に合わせる。

```sql
SELECT schemaname, tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename IN ('users', 'login_sessions', 'posts', 'realmojis')
ORDER BY tablename, indexname;

SELECT t.relname AS table_name,
       i.relname AS index_name,
       x.indisvalid,
       x.indisready,
       pg_get_indexdef(x.indexrelid) AS index_definition,
       pg_get_expr(x.indpred, x.indrelid) AS predicate
FROM pg_index AS x
JOIN pg_class AS t ON t.oid = x.indrelid
JOIN pg_class AS i ON i.oid = x.indexrelid
JOIN pg_namespace AS n ON n.oid = t.relnamespace
WHERE n.nspname = current_schema()
  AND t.relname IN ('users', 'login_sessions', 'posts', 'realmojis')
ORDER BY t.relname, i.relname;
```

索引名だけで存在を判断せず、列順・式・一意性・述語・valid/readyを確認する。複合索引が候補列を左端に含む場合や同等のpartial索引がある場合は、定義と対象クエリを照合する。`image_objects` はパーティション親だけでなく、子テーブルごとの索引・状態も個別に確認する。

### 2. テーブル規模と統計の増分を確認する

```sql
SELECT schemaname,
       relid,
       relname,
       n_live_tup,
       seq_scan,
       seq_tup_read,
       idx_scan,
       idx_tup_fetch,
       n_tup_ins,
       n_tup_upd,
       n_tup_del,
       last_analyze,
       last_autoanalyze
FROM pg_stat_user_tables
WHERE schemaname = current_schema()
  AND relname IN ('users', 'login_sessions', 'posts', 'realmojis')
ORDER BY schemaname, relname;
```

走行前後のスナップショット差分を採る場合は同じDB・同じschema/対象テーブルを使い、`relid` を含めて初期化等によるテーブルOID変更を検出する。統計resetとanalyzeの時点も記録する。`seq_scan` があることだけで問題とは判定しない。小表・低選択性・一括走査が適切なこともある。`idx_scan` は索引選択や有効性の単独証明ではない。

### 3. SQL頻度と計画を確認する

`pg_stat_statements` が既に利用可能なら、同じDB・対象について走行前後に採ったスナップショットの差分で `userid`, `dbid`, `queryid`, `toplevel`, `calls`, `total_exec_time`, `rows`, `shared_blks_hit`, `shared_blks_read` を採る。拡張がない場合は利用不可と記録し、この調査のためだけにextension有効化・再起動は行わない。代替として、アプリ側の対象SQL時間・呼出数とDBのテーブル統計を対応付ける。クエリ本文、token、username、利用者値を記録や共有資料に出さず、識別には `userid`, `dbid`, `queryid`, `toplevel` を用いる。

まず `EXPLAIN (FORMAT JSON)` 等で、実行せずに推定計画・推定行数・索引候補・パーティションpruningを確認する。実データを使った `EXPLAIN (ANALYZE, BUFFERS, TIMING OFF)` はSELECTに限定し、代表的なデータ量と選択性で実行する。`ANALYZE` は対象SQLを実際に実行するため、本番上で重いSELECTを不用意に走らせず、更新系・`FOR UPDATE` 等のロックを伴うSQLを直接実行しない。必要ならfixtureまたは負荷のない検証環境を使う。実行行数、推定との差、loops、rows removed、sort、buffer read/hit、pruningを記録する。

### 4. 採否を同条件で比較する

設定と初期状態を揃えたbaselineと索引候補を比較し、APIの対象SQL時間・呼出数、HTTP p50/p95、活動成功率、採点経路の完了数、総スコア、実現負荷を記録する。同時にINSERT/UPDATE/DELETE、初期化時間、索引サイズ、更新負荷を測る。読取計画だけが改善しても、書込み/初期化費用やスコア経路への寄与が不明なら採用を急がない。

## 制約・完了条件

- 実機確認は読み取り専用の計測を基本とし、本issueではDDL実行、索引作成/削除、DB設定変更、extension導入、ベンチ実行、ブランチ統合を行わない。
- 索引導入を検討する場合も、初期化スクリプトと既存DB適用手順を分け、追試・再起動後にも有効であることを別途検証する。DB同期耐久性、全リージョンread-after-write、権限即時反映を損なわない。
- 調査完了時は候補ごとに「不足が確認され寄与見込みあり」「既存索引/計画で十分」「寄与が小さい」「未判定」のいずれかと根拠を記録する。実装が必要なら既存の03/06/08に記録するか、異なる目的のissueを作る。調査完了は実装完了やスコア改善確認を意味しない。
