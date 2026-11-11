# main 合流による relay 追加の補償監査

## 判定

**既修正 PR #184 の queue/prune/回帰テストは main の PR #179 squash commit `8da37474aa0ff5a5f6f66b7f724dc87c8c0f28b1` に既に含まれる。`06d0916` を再 cherry-pick する必要はない。** merge 後はこの内容と既存候補の3 OS・配布受け入れ CI をともに保持する。

この判定は共通祖先からの三点比較ではなく、GitHub Contents API による各 ref の直接 blob 比較による。`compare_commits(8da3747,06d0916)` は diverged と報告し、merge-base `ff1ab67` からの追加を表示するため、現在の main との差分と誤認してはならない。

PR #184 の head は `06d0916dae9ddcfe4d721784c434958f000e28f9`。base は main ではなく `claude/project-thread-ptr1lj`、同枝への merge commit は `dbcbb2650513f5be58fddac4e4180ae92ee614b6`。その後の PR #179 squash が修正内容も収録していた。

## 直接比較 ledger

下記の blob SHA は main `8da3747` と fix `06d0916` で完全一致した。

| 対象 | 両 ref に共通の blob SHA | 保持する契約 |
|---|---|---|
| `scripts/githive-relay-pack.sh` | `0f89875c4870d0ba53f00110e1c8101ae370d4d1` | 明示 projects refspec の `fetch --prune`、remote が含む tip の除外、bundle サイズ上限 |
| `scripts/githive-relay-apply.sh` | `f248e7462886d3829bc364f811b07075ab756cd2` | advertised ref の projects prefix 検査、`fetch --prune`、bundle verify → fsck → sync |
| `scripts/test-githive-relay.sh` | `f023cd0c095f8f34581ee0a09001e77496a519ce` | 削除された remote ref の再配送と、履歴のない receiver への import |
| `.github/workflows/githive-relay.yml` | `343ef26fbc4d02a208ad5dfbb04cd4732f51cc48` | `queue: max`、`cancel-in-progress: false`、contents:write の明示 dispatch |
| `.github/workflows/ci.yml` | `0ecf03bb3eb937f6b923e96d1f8ced4bb48defc7` | Linux の `bash scripts/test-githive-relay.sh` 呼び出しを追加候補 CI と共存させる |
| `.claude/skills/githive-dogfood/SKILL.md` | `3fd094079c53290bdff8b149ab6f5377ab3142e7` | Phase 0 の対象・運用条件。新規の全機能完成保証として扱わない |

root の merge `c1ec754` 完了・Go 279 files が直前候補と byte 同一という報告は、上記 relay 追加と Go 変更を分離するための root 側証拠である。この補遺の担当者は共有 checkout の編集・commit・cherry-pick を行っていない。

## 保持すべき実証と、その限界

### prune

旧 pack を使うローカル bare fixture では、remote に一度送った ref を削除した後も tracking ref が残り、pack が終了0・出力0 bytesで「送る ref はありません」と誤判定した。

fix では初回 bundle 生成 → 既送信の省略 → remote ref 削除後の再生成を確認し、空の receiver で `git bundle verify` と fetch が成功した。旧 pack に差し替えたコピーでは同じ回帰スクリプトが FAIL、fix は PASS。証拠は元の `監査用ログ（非同梱）` と `監査用ログ（非同梱）`、追加された42行の shell regression。削除は合成 bare fixture 内だけであり、実 remote の ref 削除を行う運用手順ではない。

pack と apply の両方で prune を保持する。今回の明示 refspec は remote projects → local tracking の範囲であり、未送信 local projects ref 自体を prune する refspec に変えない。

### queue

GitHub concurrency の既定は single pending。A 実行中に B、C を送ると待機 B が取り消される。`queue: max` は待機を最大100件まで保持する。公式仕様を読んで確認したもので、3 dispatch を実際に送った試験ではない。100件超まで無制限に保持する保証もない。

参照: https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency

### 未実行を PASS としない項目

- 既修正時点では full Go test/vet/build、spec155件、shell regression、bash syntax、YAML parse、diff check が成功した。これはその時点の ref に対する証拠であり、合流後候補を自動的に PASS にしない。
- 現在はローカル PID/fork 枯渇で再実行不能。今回の追加監査は connector による内容確認であり、Go/relay dispatch を実行したとは扱わない。
- regression は pack の復旧を検証する。apply の bundle 拒否・複数 dispatch・Actions 上の roundtrip はこの回帰テストには含まれない。
- 許可された private synthetic repo への explicit sync 成功は Git transport の別証拠であり、この relay workflow の dispatch 成功の代用ではない。実 URL・raw private logs は公開資料に含めない。

## 追加された3 scripts / workflow の対象範囲

- pack は GNU `base64 -w0` を使う。対象は Linux/cloud sender と Ubuntu workflow であり、macOS の BSD base64 対応をこの実装だけで宣言しない。CI hook が Linux 限定なのはこの範囲に合う。
- apply は新しい CI checkout で local projects refs が空であることを前提に bundle tip を置く。未送信データを持つ通常の作業 clone に一般的な import コマンドとして適用しない。入口の prefix guard と fsck は役割が異なる。
- apply は init を呼ばず、既存 metadata の第2ルート作成を避ける。remote の前提 object を取得し、bundle を検査してから sync に任せる。
- workflow は unsigned Phase 0 の relay identity を設定する。署名・本番 ACL の完全な強制を提供するものではない。署名付き Hosted QA と適用条件を混同しない。
- 60,000文字の bundle 上限でエラーになる。自動分割・無制限の配送を実装したとは扱わない。

## CLAUDE / dogfood の実務適用

取得した `CLAUDE.md` の main と `7d09d0e` は blob `8f144959cd81ae1dbab5f4fc01cb3f5c850594f3`。この版には dogfood を全作業へ強制する追加文はなく、従来の merge 承認・spec/決定性・worktree 分担の指示がある。合流後のローカル版にさらに別の編集があれば root がその版を確認する。

新しい skill の適用対象は「このリポジトリの refs/projects に issue/task/chat を書くとき」。通常の read-only review や private synthetic Hosted QA の存在だけで、本体の production Hive を新規初期化・書き込みする義務は生じない。

適用する場合の要点:

1. GitHub Issues が正本。Hive は Phase 0 の試用記録として使う。
2. 既存の適切な work 領域のバイナリを使い、whoami を確認する。例示された HOME への build を、この環境で再実行する必要はない。
3. 初回 init と明示 fetch を区別する。skill の「#167 merge 前は先に fetch」は暫定条件であり、#167 は現在 main へ merge 済み（`b8f26dbbc090c324678e936b47385220cf60798e`）。固定した修正版 init の動作を使う。
4. 中継が必要な route では `--no-sync` で記録し、pack → 明示 workflow dispatch → fetch/sync。署名を既に使っている別 fixture の設定を、この Phase 0 記述を理由に無署名へ変更しない。
5. 「cloud proxy が projects push を拒否」は #176 の歴史的な Claude route 観測として読む。現在の全 provider / credential / target に対する普遍的拒否とせず、観測した route に従う。
6. skill が例示する `actions_run_trigger` という旧 tool 名は、現在発見できる専用 GitHub connector にはない。新規 dispatch は未実施で、tool があると仮定して処理済みにしない。

以上は適用条件の整理であり、今回 production Hive への書き込みや relay dispatch は行っていない。
