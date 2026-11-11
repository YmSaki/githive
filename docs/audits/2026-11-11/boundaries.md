# CLI / MCP / client 境界の補償コード監査

観測基準は `c5da0be`。本書は設計監査であり、その後の修正を既存挙動と混同しない。#189 の修正 delta は `9ed7e56`（基点64e3198）。production は下記全ファイルの責務・既定値・fallback・エラー変換を意味確認した。テストは関数/fixture構造索引を確認し、詳細担当の boundary-tests.md と分担した。テスト全件の動的実行や意味網羅を本書で主張しない。

## 主要所見

### BND-01 確定: 自動同期の成功経路で書込結果を取り違える
`cmd/githive/root.go:320` は Sync の error だけを見て、成功 Result/Warning を破棄する。`sync.go:59` の明示同期結果と異なる。remote root をpinした実fixtureで users add が ok:true の後、一覧から追加ユーザーが消える。local root pin では未配信なのに警告が消える。
根本不変条件は「local commit作成・現在refへの保持・remote配信は別の事実」。成功/失敗の二値だけでは保存できない。除去前提は構造化receiptとwarningの共通出力。第一原理案は local write、ref resolution、publication、recovery を別々に表す Outcome。オフラインでlocal保存済みの書込を成功として扱う契約は必要。
証拠は boundary-repros.json。Issue #189、F3/I7/D2/Score42。9ed7e56 は metadata保持とlocal_write_replaced、採用前のローカル専用永続保存refを導入した。reflog存在は前提にしない。

### BND-02 読み直しで通知deltaを推測する
`issue.go:340`、`mcp.go:370`、`task.go:283`、`issue.go:480`、chatのmention補助はadapterでbefore/after/actorを再読する。before読取失敗の空値代用もある。根本不変条件は「通知は同じ書込CASで実際に成立したdeltaに対応」。並行する二つの事前Showが未割当を見れば、後発no-op割当も通知できる。これはソース上の閉包不足であり、並行実行での追加実証は未実施。
共通helper化だけでは解消しない。appが同じCAS試行のactor/delta/notification intentを返し、配送の冪等性を定義することが除去前提。ネットワーク通知失敗を元書込失敗に変換しない境界は維持する。

### BND-03 確定: ファイル本文の末尾LFが失われる
`issue.go:452` の readFile は TrimRight で全末尾LFを削る。issue -F / comment -F / task note -F と鍵ファイルに同じ補償を使う。実binaryで x\\n\\n のファイルが Show では x になる。本文の入力元が本文値を変える。鍵の一行正規化と任意本文読取を分離し、本文byte保存または明示した正規化契約が除去前提。F6/I4/D1/Score24。修正未実施。

### BND-04 確定: Cobra入力エラーの分類が一般障害に落ちる
`root.go:77` と classifyError の default。実binaryの --json issue list --unknown と issue show 引数不足は exit1/code error。未知commandではJSON出力にも到達しない並びがある。docs10のusage exit2との差。parser/Args境界でtyped usage errorを返し、出力mode確定を分離する必要がある。文言regexによる推測は根本解決にならない。

### BND-05 空のack集合のCLI/MCP差
`notify.go:165` は notify ack --all の空一覧を一般エラーにし、MCP notify_ack のall分岐は acked:[] 成功。CLI側は実binary確認、MCPは分岐確認。欠落した必須引数と、正当な空集合への冪等操作を区別し、shared app結果へ統合することが除去前提。明文化された差分契約は未確認。

### BND-06 到達不能なlenient helper
`whoami.go:80` matchRegistryUser、`users.go:391` resolveUserRefLenient はエラーを空値に変えるが、索引上definition以外の参照がない。現行read-view境界の迂回としては報告しない。呼出しゼロとtest影響を確認して削除できる候補。

## 残すべき境界と除去前提

- clioutのcanonical encoder、ItemsMap正規化、PrintSuccessのencode失敗伝播は必要。失敗時にdataだけ省くfallbackは成功を捏造しない。共通typed wire DTOがcanonical byteを維持するまでは独立JSON実装へ置換しない。
- rootのtyped error taxonomyと部分sync receiptは必要。単一のtyped Outcomeがerror/data/warningsを一緒に写像できるまでは消せない。
- read_viewのsnapshot/history_complete:false/verified:false と mutationのfull-history resolver分離は信頼境界。rendered Markdownを元bodyと偽らない。完全履歴またはlossless表現と検証なしに共通Foldへ統合しない。
- wikiのUTF8/base64分岐は可逆出力のため必要。resource URIの一度だけのunescapeと限定したnotfound写像も必要。
- MCPのstartup remote固定と指定remote照合は権限境界。pagination上限もcontext制御。CLI/MCP autosync非対称はADR0011の明示契約。
- MCP verify/fsck/doctor はdomain verdict data.ok と transport IsErrorを区別する。data.ok:falseを成功監査と解釈しないconsumer契約が必要。共通AuditVerdict/ExecutionStatusを定義するまでは一律IsError化も単純削除も不適切。
- users policyの編集失敗時draft保持、editor stdoutのstderr転送、初期denyは必要な障害処理/ドメイン既定値。
- repoDirの.git存在probeはGit能力の近似。偽.gitでidentity_not_configuredとなることは確認したが、書込の偽成功は確認していない。git capability検査へ変更する場合も曖昧な別repo fallbackを導入しない。
- pluginのinit/sync/whoami/status、projectdir固定は明示setup。Codex plugin未提供はREADMEの既知範囲であり、機能完成を推定しない。

## Production coverage ledger

各行はsemantic review済。テストfixture/mock/skipの構造確認は boundaries-test-index.txt と boundary-tests.md へ分離。core/app は他担当のため対象外。生成物/依存ライブラリも対象外。

| file | 確認した責務・補償 |
|---|---|
| cmd/githive/chat.go | CRUD/mention/返信/ID表示、通知失敗分離、tail |
| cmd/githive/doctor.go | 診断verdictとexit、repair呼出し |
| cmd/githive/fsck.go | 診断/修復/再検査とexit |
| cmd/githive/init.go | 初期化、identity/bootstrap、sync、warnings |
| cmd/githive/issue.go | CRUD/状態/割当/本文入力、通知推測、ID解決 |
| cmd/githive/log.go | event履歴/pagination/表示 |
| cmd/githive/mcp.go | tool登録全handler、remote pin、resource、pagination、error/warning写像 |
| cmd/githive/mcp_wiki_resource.go | URI decode/read-only resource/notfound |
| cmd/githive/notify.go | post/list/ack、空集合、ID、sync |
| cmd/githive/notify_hooks.go | 通知intentと失敗warning化 |
| cmd/githive/read_view.go | snapshot由来と未検証marker、body projection |
| cmd/githive/root.go | repo/context/flag/default、error taxonomy、autosync |
| cmd/githive/status.go | 複数stream集約とread-view |
| cmd/githive/sync.go | 部分結果、action/warnings/metadata adapter |
| cmd/githive/task.go | mutation/before state/notify/ID/date |
| cmd/githive/users.go | policy editor/draft、identity、lenient dead helper |
| cmd/githive/verify.go | diagnostic、エラー優先順位、exit |
| cmd/githive/whoami.go | identity resolverとdead fallback |
| cmd/githive/wiki.go | page入力、binary表現、notfound |
| internal/cliout/cliout.go | canonical encode、items coercion、失敗fallback |
| clients/plugins/claude/.claude-plugin/plugin.json | plugin metadata/entrypoint |
| clients/plugins/claude/.mcp.json | server起動とproject binding |
| clients/plugins/claude/README.md | install/current範囲/利用前提 |
| clients/plugins/claude/skills/githive-usage/SKILL.md | agent workflowとverdict判定、明示sync |

基準revではproduction24、test41。後続boundary-tests.mdは42 filesで、epoch差分を含むため数字を一致させて網羅性を偽装しない。runtime実証のコマンドstdout/stderr/statusとartifact SHAは boundary-repros.json / boundary-provenance.json を参照。最終fixのGC/backup拒否/CAS runtime試行は環境fork枯渇でblocked、compile到達と実行PASSを区別した。

