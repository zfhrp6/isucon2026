# asia1 Go CPU profileのstack帰属

## 管理状況

- 状態: **調査済み（CPU stackの上位経路を特定）**
- 対応範囲: 一時`diagnostic_pprof` buildでasia1のCPU profileを取得し、Go CPU samplesをstackへ帰属する。
- 実装コミット/記録: 診断用の一時binaryは元binaryへ復元済み。復元後の稼働中実行ファイルのSHA-256は導入前と一致。
- 検証済み/未検証: 8区間のCPU profileを読み、活動の強い4区間でstackを集約した。採点区間への厳密な対応、activity成功率、score因果、割当byte、CPU/RSSの同時推移は未確認。

## 結論

観測したasia1 Go CPU samplesの大半は画像URL署名計算だった。活動の強い4区間では`imageSignature`以下が68.18秒（107.78秒の63.26%）、JPEG decodeが18.93秒（17.56%）。署名処理を呼ぶhandlerではMemories、export、feedが主な経路だった。HMAC loopの仕様を維持した初期化・buffer再利用を最初に隔離比較し、投稿作成のJPEG先行寸法判定は契約維持テストと入力別計測を伴う別候補として扱う。

このprofileはGoプロセスのCPU sampleで、DB・ネットワークの待ち時間や、各処理の実行request数を測らない。profile取得時刻と競技benchmarkの採点フェーズの対応も検証していない。profile有効/無効の同条件走行を比較していないため、pprof自体のoverheadも未測定。走行時点の実効environment/config・DB schema/statistics snapshotまたはdigestは保存していない。これは「設定やDB状態が変わらなかった」という意味ではなく、source/binary hashだけから再現可能な状態を復元できない。従って、asia1のCPU sampleに対する処理割合をscore寄与や実現可能な節約率と読み替えない。

## 実行物と採取条件

| 項目 | 値 |
|---|---|
| Host / process | `isu-asia1` / Go `isureal-go` |
| Source revision | 取得元HEAD `5af79074a710a0bdb65992889b459b81fc22998a`。このcommitへ診断用build tagを加えて一時buildした |
| Diagnostic binary | Build ID `513d2dea17762a68ac4c1fff7a440dc3d366d766`、SHA-256 `a1597b9823f6888cb02d4885931f08a9321f9730023e0ba64c5a2de279e4ed07` |
| Build toolchain | Go `1.26.5`, `linux/amd64`。保存binaryの`go version -m`出力と照合可能 |
| Restored binary | SHA-256 `77aa3c8f12cc7842eb7df809da32adcfa4e62218fdb111560e500cfb2f14a3c5`。`/proc/<MainPID>/exe`でも同一hash、service active、6060 listenなし、API直結とnginx経由が導入前同様401を確認 |
| Profile period | 10,000,000ns（100 samples/s） |
| Profile status | 8本は取得済み。9本目は復元時のservice restartで中断し未完成のため削除 |

計測開始は2026-10-31 06:17:30.692 UTC。各ファイルは30秒の窓で、CPU samplesは最初の2本が0、3〜6本目に99.31%が集中、最後の2本は合計0.75秒だった。活動の強い3〜6本目は `[06:18:31.213351531Z, 06:20:32.717093747Z)` を覆い、窓間のgapは合計約1.486秒。100%は1 CPU相当である。最大窓は30秒のwall timeよりCPU samplesが多く、並行goroutineのCPU実行を含む。CPU数、steal/iowait、host全体の飽和は未計測なので、これだけではhostの全CPUが飽和したとは言えない。

| Profile | UTC開始 | CPU samples | CPU時間 / wall time | imageSignature stack / share |
|---|---|---:|---:|---:|
| `cpu-1` | 06:17:30.692 | 0 | 0s / 30.000s | 0s |
| `cpu-2` | 06:18:00.960 | 0 | 0s / 30.000s | 0s |
| `cpu-3` | 06:18:31.213 | 472 | 4.72s / 30.000s (15.73%) | 4.11s (87.08%) |
| `cpu-4` | 06:19:01.487 | 2,529 | 25.29s / 30.001s (84.30%) | 16.23s (64.18%) |
| `cpu-5` | 06:19:32.297 | 5,048 | 50.48s / 30.015s (168.18%) | 29.93s (59.29%) |
| `cpu-6` | 06:20:02.716 | 2,729 | 27.29s / 30.001s (90.96%) | 17.91s (65.63%) |
| `cpu-7` | 06:20:33.293 | 60 | .60s / 30.001s (2.00%) | 0s（symbolized top-cumに該当stackなし） |
| `cpu-8` | 06:21:03.547 | 15 | .15s / 30.000s (.50%) | 0s（symbolized top-cumに該当stackなし） |

この分布から負荷活動が3〜6本目に集中していたことは分かるが、benchmark runnerの採点時刻が記録されていないため、4区間を公式採点フェーズと断定しない。

統合した4本のprofileは120.017276670秒、総CPU samples 107.78秒である。主要stackは以下の通り。flatは関数自身、cumはcalleeを含む値であり、同じstackの行を足し合わせない。

| Stack / 関数 | CPU samples | 全体比 | 読み方 |
|---|---:|---:|---|
| `imageSignature` subtree | 68.18s | 63.26% | この関数が属するsample stack |
| `signedImageURL` subtree | 63.55s | 58.96% | URL生成と下位署名処理を含むinclusive値。その内63.43sが署名生成。画像取得側の署名検証4.75sは別handler/別経路 |
| `crypto/hmac.New` | 31.52s | 29.24% | `imageSignature` subtree内のHMAC初期化。上の68.18sに含まれる |
| `image/jpeg.Decode` subtree | 18.93s | 17.56% | `createPostHandler`内18.86s。profile-image PUT .05s、RealMoji .02s |
| `runtime.mallocgc` subtree | 15.27s | 14.17% | mutator側allocation/assist等を含むinclusive値 |
| `runtime.gcBgMarkWorker` subtree | 4.32s | 4.01% | concurrent GC worker。今回のstack-unionではmallocgc stackとは別sample群 |
| `runtime.gcAssistAlloc` subtree | 2.48s | 2.30% | mutator allocation assist。mallocgc subtreeとの重複を含むため加算しない |

対象profileのstack unionでは`mallocgc`とbackground mark workerが重複せず、両方を合わせたdistinct CPUは19.59秒（18.18%）。署名処理stackとmallocgcは12.42秒で重複し、JPEG decode stackとmallocgcは.21秒で重複した。署名とgc-assistは1.78秒、JPEGとgc-assistは.03秒重なる。重複分を各行から足さない。画像署名の最適化がallocation/GC圧にも波及する可能性はあるが、heap bytesやallocs countをこのCPU profileから推定しない。

## API別stack

呼出元の処理時間を、route全体のHTTP latencyやrequest数とは扱わない。

| Handler | Go CPU samples | 主な内訳 |
|---|---:|---|
| `getMemoriesHandler` | 35.07s | 署名34.71s、mallocgc6.67s。Memories response構築のCPU |
| `createPostHandler` | 20.49s | JPEG18.86s、署名.82s、mallocgc.47s |
| `exportPostsHandler` | 20.47s | 署名19.85s、mallocgc3.26s |
| `getFeedHandler` | 5.99s | 署名5.47s、mallocgc1.30s |
| `getImageHandler` | 5.82s | 画像署名検証4.75s、mallocgc1.11s |
| `searchUsersHandler` | 4.53s | `sqlx.SelectContext`2.99s（scan 2.97s）、`normalizeSearchText`1.45s、mallocgc1.42s |
| `putProfileImageHandler` | 3.47s | `io.ReadAll`3.41s、JPEG decode.05s、mallocgc.19s |

この表はstack subtreeによるhandler別のCPU帰属で、DBのquery elapsedや応答所要時間ではない。`ActionLogMiddleware`のcum100.91秒はHTTP handler全てを呼ぶmiddlewareの祖先stackであり、ActionLogがそのCPUを消費したという意味ではない。DB driver stack4.88秒もDB serverのCPUやDB待ちの値ではない。

Memories月・日handlerは35.57秒（getMemories 35.07s + getMemoriesByDate .50s）で、署名stack35.19秒だった。投稿responseを組み立てる`newPostResponse`はfeed、Memories、export等から呼ばれる共有helperであり、handlerのinclusive値に重ねて別加算しない。exportは独立したhandlerでMemoriesの一覧関数を呼ばず、各投稿に同じ`newPostResponse` helperを使用する。

## 実装候補と受け入れ条件

以下はprofile取得時の旧実装から挙げた候補。現HEADでは`69d6365`のHMAC state/buffer再利用が統合済みである。保存profileの履歴は変更せず、実装の現状は[issue 17](17-image-signature-cpu.md)、要求内の重複署名削減は[issue 34](34-response-image-signature-reuse.md)へ分ける。統合済みであることは、このprofile取得時点の値が現在も同じであることや、実機で得点効果を確認したことを意味しない。

1. `imageSignature`の1000 roundを保ち、HMAC objectの再初期化・buffer再利用を局所benchmarkで比較する。並行呼出しでbufferを共有しない。署名バイト列、有効期限、key管理、検証、削除/認可の反映は変更しない。API request数・同一画像URLの重複率をログ/benchmarkから別計測し、request単位に帰属しないCPU/allocsを無理に割り当てない。
2. `createPostHandler`は寸法を確認するためにJPEGを完全decodeする。`DecodeConfig`による上限超過の早期拒否は候補だが、受理寸法での完全decodeは維持し、壊れ/途中切れデータの拒否を変えない。profileだけでは画像サイズや件数が不明で、decode全体の17.56%が節約できるとは言えない。HTTP bodyのサイズ上限適用は別契約・リスクも検討する。
3. 施策ごとに同じ初期状態・実効binary/configのbaselineを取り、CPU/allocs profile、route別API p50/p95、件数と成功率、投稿後の期限内検証、scoreを比較する。DB/pool/lock待ちとOS CPU/iowaitも同一時間窓で照合し、CPU改善だけで待ちや採点の改善を主張しない。

## 再現とartifact

raw profile、分析出力、diagnostic binaryはGit管理外の `logs/pprof/20261031T061729Z.scglyb/` に保持した。raw dataは本体workspaceにのみあり、Gitに含まれないため別checkoutでは参照できない。以下はrawが置かれたrepository rootから再集計する例。出力先は`mktemp`で別名を確保し、既存`cpu-load.pprof`を上書きしない。`go tool pprof`がGo toolchainに含まれていない場合は、利用できる独立`pprof` executableにコマンドprefixを置き換える。

```sh
profile_dir=logs/pprof/20261031T061729Z.scglyb
profile_binary="$profile_dir/isureal-diagnostic"
rebuilt_profile=$(mktemp /tmp/asia1-cpu-load.XXXXXX.pprof)
go tool pprof -proto \
  "$profile_binary" \
  "$profile_dir/cpu-3.pprof" \
  "$profile_dir/cpu-4.pprof" \
  "$profile_dir/cpu-5.pprof" \
  "$profile_dir/cpu-6.pprof" > "$rebuilt_profile"

go tool pprof -top -cum -nodefraction=0 -nodecount=0 \
  "$profile_binary" "$rebuilt_profile"
go tool pprof -peek='imageSignature|signedImageURL|image/jpeg.Decode' \
  "$profile_binary" "$rebuilt_profile"
go tool pprof -top -cum -nodefraction=0 -nodecount=0 \
  "$profile_binary" "$profile_dir/cpu-7.pprof"
```

`-nodefraction=0 -nodecount=0`で小さいnodeを含めて表示する。flat/cumを比べるときは`-flat`または`-cum`を指定し、inclusive値の合計でCPUを二重計上しない。今回のローカル解析toolは一時取得物なので再現時の依存にしない。

| Artifact | SHA-256 |
|---|---|
| `isureal-diagnostic` | `a1597b9823f6888cb02d4885931f08a9321f9730023e0ba64c5a2de279e4ed07` |
| `cpu-load.pprof` (profiles 3–6 merged) | `788eb1b84ec2c437ccea73fc7aa8fe96eba9d63785afd7f7cc778164f9ce0aa3` |
| `cpu-load.svg` | `72a46ee32c5af699c2600bc840edfa829f4f3b2499daecdf0fd397ac438b4905` |
| `cpu-1.pprof` | `18abab9cebe25034827e3c30f1e52bfa06bb92a669e30eaa96d080720fe310df` |
| `cpu-2.pprof` | `e7cab2ad8f268c9c6c32f6ef0aa56b27e9a32fb85977b397fd5204e77dd98881` |
| `cpu-3.pprof` | `5aaef494ab259fbed3b24942e545c49cadf9a3504057f5910fbf34f3ec335f2b` |
| `cpu-4.pprof` | `fcaadecaee4d06d0bdefbc60a7eb56323201fbf3397ce3f3348e2c7ea983480f` |
| `cpu-5.pprof` | `61e6e25b0d0e2a55768583e42478412b8fddb6f705524511814aa704c7b36f38` |
| `cpu-6.pprof` | `05d48ce2f99021495a11ae86758235e93208c891903eb9c4bf1c0b33d266d811` |
| `cpu-7.pprof` | `dc15e577fc1f9b708aca7a6869eaf6a41a3b5e84622b9cbe52d40920a620f9ef` |
| `cpu-8.pprof` | `9a188226f370f59443d3e2c7a09ce9ff5278c13a12e673b93a6fc944a9691216` |

CPU profile取得scriptの30秒窓9本目はservice restore時に切断され、不完全ファイルを削除した。rawの完全な8本とは区別する。この分析は一度のprofileであり、再現性や得点効果を示さない。次runではscoring timestampとprofile window、effective artifact/config、activity success/score、CPU全体とDB waitを相関付けてから実装採否を判断する。
