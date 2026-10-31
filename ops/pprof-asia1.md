# asia1 の一時 pprof

`ops/pprof-asia1.sh` は asia1 の Go 実行ファイルだけを診断用 build tag 付きで置き換える。pprof は `127.0.0.1:6060` のみで待ち受け、DB・環境変数・nginx・他ホストは変更しない。標準の `ops/deploy.sh` は通常ビルドを配備するため、この一時設定中に実行しないこと。

普段は `start` → ベンチマーク → `stop` の2コマンドで済む。診断記録とprofileは本体worktreeの `logs/pprof/` に保存する。`start` は省略時20窓、指定時1〜20窓のCPU/allocs収集をバックグラウンドで開始し、最初のprofileディレクトリと `allocs-before.pprof` が準備できてから戻る。診断バイナリを実行中プロセスの `/proc` でも照合し、loopback pprofと `/api/me` の未認証応答401も確認する。linked worktreeから実行しても本体側のactive記録を共有する。開始が失敗または不確定になった場合も復旧記録を残すので、消さずに `stop` を実行すること。すでに記録がある状態での `start` は拒否される。

```sh
bash ops/pprof-asia1.sh start
# ベンチマークを実行
bash ops/pprof-asia1.sh stop
```

`start [windows]` は診断バイナリのSHA-256、ソースcheckoutのパスとHEADもローカルに記録する。`stop` はcollectorに現在の最大30秒CPU窓の完了を依頼し、最終allocsを保存してから診断版を復元する。collector異常時も復元は試み、異常を終了コードとcollector logで報告する。復元後のサービス稼働、実行中プロセスの元バイナリSHA、pprofポート閉鎖、APIの401応答を確認してからセッション記録を消す。collectorの完了を55秒以内に確認できない場合はサービスを復元してもactive記録を残す。collector終了後に `stop` を再実行して記録を解消する。測定ごとの状態は `/home/isucon/.isureal-pprof-diagnostic.<measurement-id>/` に保存され、復元後も自動削除しない。取得ディレクトリも従来どおり保持する。

別worktreeのcheckoutを測る場合は、最新スクリプトへ`--source-root <checkout>`を渡す。同じGitリポジトリのcheckout rootだけを受け付ける。ソースは指定先からビルドし、記録・ログは引き続き本体worktreeに保存する。対象ブランチに最新スクリプトをコピーする必要はない。

```sh
bash ops/pprof-asia1.sh --source-root /home/zfhrp/workspace/isucon2026_work1 start
bash ops/pprof-asia1.sh stop
```

対象ブランチの通常デプロイを先に完了させてからpprofを有効化する。診断中に通常デプロイを実行するとpprofは無効になり、バイナリが変わるため復元も安全のため拒否される。

個別IDを指定する復旧・詳細操作も残している。`enable` は呼び出し側が事前確認した現行バイナリのSHA-256を必須引数として受け取り、配備直前にも照合する。同じIDでの再導入は拒否し、過去の測定のバックアップは上書きしない。初回測定の旧保存先 `/home/isucon/.isureal-pprof-diagnostic/` も変更しない。

自動collector稼働中の `capture-current` は二重CPU profileを避けるため拒否する。手動でprofileを追加取得する場合はcollectorが動いていないことを確認してから低レベルの `capture <measurement-id>` を使う。

```sh
# 測定IDと、実機で事前確認した現行バイナリのSHA-256を指定する。
bash ops/pprof-asia1.sh enable <measurement-id> <expected-original-sha256>
bash ops/pprof-asia1.sh status <measurement-id>
bash ops/pprof-asia1.sh capture <measurement-id> 20
bash ops/pprof-asia1.sh restore <measurement-id>
```

`capture <measurement-id> 20` は30秒CPU profileを最大20本収集し、`logs/pprof/<UTC時刻>.<measurement-id>.<一意な識別子>/` に保存する。実行中プロセス・保存した診断バイナリのハッシュが指定した測定と一致することを確認してから、解析用バイナリも回収する。開始時の`allocs-before.pprof`と、各CPU窓の直後に更新する`allocs-after.pprof`も保存する。allocs取得の間はCPU採取の隙間があり、全体は約10分となる。割り当てprofileはサンプリング推定で、GCによる反映遅延もあるため、差分を正確な割り当てバイト数や特定APIの量と同一視しない。測定有無のoverhead比較は別途必要。

途中で止める場合は実行中のローカルシェルで Ctrl-C を押す。profileはGit管理外の診断データとして扱う。CPU profileは取得成功後に確定名へ移動するため、失敗した取得が完成済みファイルとして残らない。ベンチ終了後に復元して収集中のCPU窓が切断された場合も、直前までの完成したCPU窓とallocsを保持する。必要なら復元直前にallocsをもう一度回収する。

復元は指定した測定IDの元実行ファイルとそのSHA-256を確認してから行う。診断版と元のどちらとも一致しない場合は、別の配備を上書きせずに失敗する。元binaryへ交換後にサービス再起動が失敗しても、同じ測定IDのrestoreで再起動をやり直せる。測定状態は復元後も自動削除しない。復元を確認する前にこの状態を削除しないこと。`status` は元binary・サービス稼働・pprofポート閉鎖を確認して復元済みと表示する。
