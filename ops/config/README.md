# サーバー設定

このディレクトリには、2026-10-31 01:34 UTC に各インスタンスから読み取り専用で取得した設定を保存する。初回取得時は15ファイルすべてで、実機のファイルとリポジトリ内ファイルの SHA256 が一致した。アプリケーションサーバー3台の nginx と logrotate 設定は同一だった。その後、nginx の3設定には alp 用アクセスログ形式への変更を加えている。実機へはまだ反映していない。

| リポジトリ内のファイル | 取得元・配置先 | 所有者・モード |
|---|---|---|
| `nginx/<host>/nginx.conf` | 各アプリサーバー `/etc/nginx/nginx.conf` | `root:root 0644` |
| `nginx/<host>/isureal.conf` | 各アプリサーバー `/etc/nginx/sites-available/isureal.conf` | `root:root 0644` |
| `nginx/<host>/logrotate.conf` | 各アプリサーバー `/etc/logrotate.d/nginx` | `root:root 0644` |
| `postgresql/isu-asia2/postgresql.conf` | DBサーバー `/etc/postgresql/16/main/postgresql.conf` | `postgres:postgres 0644` |
| `postgresql/isu-asia2/pg_hba.conf` | DBサーバー `/etc/postgresql/16/main/pg_hba.conf` | `postgres:postgres 0640` |
| `postgresql/isu-asia2/pg_ident.conf` | DBサーバー `/etc/postgresql/16/main/pg_ident.conf` | `postgres:postgres 0640` |
| `postgresql/isu-asia2/conf.d/isucon.conf` | DBサーバー `/etc/postgresql/16/main/conf.d/isucon.conf` | `postgres:postgres 0644` |
| `postgresql/isu-asia2/start.conf` | DBサーバー `/etc/postgresql/16/main/start.conf` | 確認用に追跡。反映対象外 |
| `postgresql/isu-asia2/pg_ctl.conf` | DBサーバー `/etc/postgresql/16/main/pg_ctl.conf` | 確認用に追跡。反映対象外 |

nginx の有効サイト `/etc/nginx/sites-enabled/isureal.conf` は、管理対象の `sites-available/isureal.conf` を参照する既存シンボリックリンク。スクリプトはリンク先を検証し、リンク自体は変更しない。nginx の `conf.d`、`modules-enabled` と PostgreSQL の `environment` にはコピーが必要な有効設定はなかった。nginx はパッケージ管理下の `/etc/nginx/mime.types` を参照する。

## nginx アクセスログを alp で分析する

Git 管理している nginx 設定では、3台ともアクセスログを LTSV にする。`access.ltsv.log` には `time`、`host`、`method`、`uri`、`status`、`size`、`reqtime`、`apptime` をこの順に出力する。時間とサイズの値は秒・バイト単位。静的ファイルなど upstream がない応答では `apptime` は `-` になる。ログにはリクエスト本文や Cookie、認証ヘッダーを含めない。

この変更は初回収集時の設定には含まれない。新形式のログファイルができるのは nginx 設定を実機へ反映した後なので、先に dry-run の対象を確認し、設定変更を反映する。

```sh
./ops/deploy-config.sh nginx --dry-run
./ops/deploy-config.sh nginx
```

反映後、現在のアクセスログとエラーログを各アプリサーバーから取得する。

```sh
./ops/fetch-nginx-logs.sh
```

ログは `logs/nginx/<UTC日時>-<識別子>/<host>/` 以下にホストごとに保存される。次の `<run>` は実際に作成された取得ディレクトリ名に置き換える。

```sh
alp ltsv --file logs/nginx/<run>/isu-asia1/access.ltsv.log --sort sum --reverse
```

`--sort sum --reverse` は集計時間の長い URI から表示する。既定では upstream 応答時間 `apptime` を使い、upstream がない行では `reqtime` にフォールバックする。リクエスト全体の時間で揃えて集計する場合は `--apptime-label reqtime` を指定する。

```sh
alp ltsv --file logs/nginx/<run>/isu-asia1/access.ltsv.log --sort sum --reverse --apptime-label reqtime
```

クエリ文字列を集計に含めたい場合は `-q` を指定できる。キー名は alp の LTSV 既定値を使う。`error.log` は LTSV 形式ではなく、障害診断用として別に保存する。反映前に fetch を実行すると新形式のファイルがまだないため、スクリプトは取得に失敗する。形式は [alp のログ形式](https://github.com/tkuchiki/alp#log-format)と [nginx の access log 設定](https://nginx.org/en/docs/http/ngx_http_log_module.html)に合わせている。

## PostgreSQL の遅いSQL・ロック待ちログを取得する

PostgreSQL 16/main向けの追加設定は [`conf.d/isucon.conf`](postgresql/isu-asia2/conf.d/isucon.conf) に置く。`listen_addresses` は既存値を保持し、100ms以上のSQL、`deadlock_timeout`以上（現行既定値では1秒以上）のロック待ち、接続/切断をstderrログへ記録する。接続記録には接続先などの情報が含まれる場合がある。バインドパラメーターのログ長は通常時・エラー時とも0にするが、SQL文字列に直接書かれたリテラルやエラーのcontextまで除去する保証はない。遅いSQLログやエラーには識別情報が含まれる可能性があるため、取得物は機密ログとして扱う。

`log_statement=all` は有効化せず、`deadlock_timeout`、`logging_collector`、ログ出力先は変更しない。現在のDebian/Ubuntu構成では `logging_collector=off`、`log_destination=stderr` で、PostgreSQL 16/mainのログは `/var/log/postgresql/postgresql-16-main.log` に出る想定。collector無効時は `pg_current_logfile()` がNULLになり得るため、取得先の決定にはDebianの `pg_lsclusters` と固定したクラスタ/パスの検証を使う。スクリプトは別のログ配置を自動探索せず、想定と異なる環境では失敗する。

設定反映前に対象を確認し、反映後に現在ログを取得する。

```sh
./ops/deploy-config.sh postgresql --dry-run
./ops/deploy-config.sh postgresql
./ops/fetch-postgresql-logs.sh --dry-run
./ops/fetch-postgresql-logs.sh
```

設定はreload可能な範囲だけを追加する。PostgreSQL 16での隔離検証では `log_connections` と `log_disconnections` のcontextは `superuser-backend` で、reloadは再起動を要しなかった。接続記録の対象は設定reload後に開始する接続であり、既存セッションへ遡及しない。取得スクリプトは `isu-asia2` の稼働中16/mainだけを対象にし、接続時にクラスタ、実効設定、ログファイルをread-onlyで確認する。実機ログは `logs/postgresql/<UTC日時>-<識別子>/isu-asia2/postgresql.log`、設定メタデータは同じ場所の `settings.tsv` に保存する。ログ取得前の設定値も記録するため、負荷走行前後で条件を照合できる。初期状態・走行後など必要な時点で個別に取得し、収集間隔は比較記録へ残す。

ユーザー観測ではベンチ中に米州・欧州のCPU使用率は低めで、asia1アプリまたはasia2 PostgreSQLのCPU使用率が高く、asia2のPostgreSQL `SELECT`/`BIND` processも高CPUに見えたとのこと。時刻・定量値・走行条件は未確認。100ms閾値の遅いSQLログだけでは高頻度の短いSQLを見落とし、statementログだけでBIND/Execute各段階のCPUを分離できないため、それぞれ別に計測する。nginx/アプリの活動時刻、DBログ、同じ時間窓のCPU・DB統計を照合し、`pg_stat_statements` が既に使える場合は集計差分も確認する。単独のログやCPU観察だけで原因を決めない。

取得物は `.gitignore` 対象で、ローカルもディレクトリ0700・ファイル0600で保存する。スクリプトは現在のログファイルだけを読み、過去のrotate済みログは取得しない。サーバーが追記・rotate中に読む可能性があるため、厳密な原子的スナップショットではない。`--dry-run` はSSH接続やファイル作成をしない。SSHはBatchModeと接続timeoutを指定し、リモートではログ削除・truncate・reload等を行わない。

PostgreSQL のメイン設定は `conf.d` を読み込む。取得時の有効な設定元は `postgresql.conf` と `conf.d/isucon.conf` だけだった。`postgresql.auto.conf` に有効な設定はなかったため、生成される実行時ファイルとして管理対象外にしている。反映時には、想定外の設定元や `postgresql.auto.conf` に有効な項目があれば処理を拒否する。SSL はホスト側の snakeoil 証明書と秘密鍵を参照するため、どちらもコピーしていない。パスワードや秘密鍵などの秘密情報も含めていない。

## 設定を反映する

最初に対象を確認する。

```sh
./ops/deploy-config.sh nginx --dry-run
./ops/deploy-config.sh postgresql --dry-run
```

変更するコンポーネントを指定して反映する。

```sh
./ops/deploy-config.sh nginx [isu-asia1 ...]
./ops/deploy-config.sh postgresql [isu-asia2]
```

ホストを省略すると、nginx はアプリサーバー3台、PostgreSQL は共有DBサーバー1台が対象になる。スクリプトはホストとローカルファイルをすべて確認してから接続し、指定したファイルだけを転送する。同一ホストでの並行実行をロックで防ぐ。変更前に各ホストの `/var/backups/isureal-config/` 以下へ root 専用バックアップを作り、その場所を表示する。複数ホストは順番に処理する。後のホストで失敗しても、先に成功したホストの変更は残るため個別に状態を確認する。

nginx は設定ファイル3個を取得時の所有者・モードで配置し、`nginx -t` と `logrotate --debug` が成功した後に nginx を reload する。稼働状態も確認する。失敗時にはファイルを復元し、reload 後の失敗なら復元した設定の再読込も試す。サービスの再起動はしない。

PostgreSQL は有効な設定ファイルのパスを確認する。未管理の設定元または `postgresql.auto.conf` の有効設定がある場合は処理を拒否する。対象4ファイルを配置した後にも同じ条件を確認し、reload 前に `pg_file_settings`、`pg_hba_file_rules`、`pg_ident_file_mappings` を検査する。エラーがあれば reload せず、以前のファイルへ戻す。検査に成功した場合は PostgreSQL の systemd unit から reload し、最大15秒、`pg_conf_load_time()` が更新されるのを待つ。その後、設定エラー、`pending_restart`、クラスタの稼働状態を確認する。DB は自動再起動しない。再起動が必要な変更は事前検査で拒否する。reload 後の確認に失敗した場合は以前の設定を戻し、systemd から再度 reload を要求する。ファイルの復元や再読込も失敗した場合、バックアップ先とエラーを表示する。

## バックアップから復元する

バックアップは各ホストに残る。以前の設定を含むため root 専用のまま保管する。以下の `BACKUP_DIR` は、スクリプトが表示した実際のパスに置き換える。

nginx は対象アプリサーバーで実行する。

```sh
sudo cp -a "$BACKUP_DIR/nginx.conf" /etc/nginx/nginx.conf
sudo cp -a "$BACKUP_DIR/isureal.conf" /etc/nginx/sites-available/isureal.conf
sudo cp -a "$BACKUP_DIR/logrotate.conf" /etc/logrotate.d/nginx
sudo nginx -t && sudo systemctl reload nginx
```

PostgreSQL は `isu-asia2` で実行する。

```sh
sudo cp -a "$BACKUP_DIR/postgresql.conf" /etc/postgresql/16/main/postgresql.conf
sudo cp -a "$BACKUP_DIR/pg_hba.conf" /etc/postgresql/16/main/pg_hba.conf
sudo cp -a "$BACKUP_DIR/pg_ident.conf" /etc/postgresql/16/main/pg_ident.conf
sudo cp -a "$BACKUP_DIR/isucon.conf" /etc/postgresql/16/main/conf.d/isucon.conf
sudo systemctl reload postgresql@16-main
```

復元後はサービス状態と設定エラー表示を確認する。reload 成功だけでは、HTTPやDBクライアントの動作は確認できない。

PostgreSQL の事前検査と reload には、公式の[設定検査・reload 関数](https://www.postgresql.org/docs/16/functions-admin.html)と[設定値・include の仕様](https://www.postgresql.org/docs/16/config-setting.html)を利用している。

設定は実機から読み取り専用で収集し、内容を検証した。競技サーバーへの反映はまだ行っていない。
