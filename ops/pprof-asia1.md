# asia1 の一時 pprof

`ops/pprof-asia1.sh` は asia1 の Go 実行ファイルだけを診断用 build tag 付きで置き換える。pprof は `127.0.0.1:6060` のみで待ち受け、DB・環境変数・nginx・他ホストは変更しない。標準の `ops/deploy.sh` は通常ビルドを配備するため、この一時設定中に実行しないこと。

この実行scriptは今回の一度限りの診断用である。`enable` は事前確認した実行ファイルSHA-256を固定して拒否条件に使い、復元後もホストに元binaryと状態を保持する。したがって、次回走行への一般的な切替手順として使い回さず、別の診断ではその時点の配備binaryを確認した専用の手順を用意する。

```sh
bash ops/pprof-asia1.sh enable
bash ops/pprof-asia1.sh status
bash ops/pprof-asia1.sh capture 20
bash ops/pprof-asia1.sh restore
```

`capture 20` は30秒CPU profileを連続して最大10分収集し、`logs/pprof/<UTC時刻>.<一意な識別子>/` に保存する。同じディレクトリへ診断用実行ファイルも取得し、profileのシンボル解決に使えるようにする。途中で止める場合は実行中のローカルシェルで Ctrl-C を押す。profileはGit管理外の診断データとして扱う。各profileは取得成功後に確定名へ移動するため、失敗した取得が完成済みファイルとして残らない。

復元は保存した元実行ファイルとそのSHA-256を確認してから行う。診断版と元のどちらとも一致しない場合は、別の配備を上書きせずに失敗する。元binaryへ交換後にサービス再起動が失敗しても、次回のrestoreで再起動をやり直せる。サーバー上の状態は `/home/isucon/.isureal-pprof-diagnostic/` に保持し、復元後も自動削除しない。復元を確認する前にこの状態を削除しないこと。`status` は元binary・サービス稼働・pprofポート閉鎖を確認して復元済みと表示する。
