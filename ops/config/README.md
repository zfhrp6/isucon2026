# サーバー設定

このディレクトリには、2026-10-31 01:34 UTC に各インスタンスから読み取り専用で取得した設定を保存する。コピーした15ファイルすべてで、実機のファイルとリポジトリ内ファイルの SHA256 が一致した。アプリケーションサーバー3台の nginx と logrotate 設定は同一だった。

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
