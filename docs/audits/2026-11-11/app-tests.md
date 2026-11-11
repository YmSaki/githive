# app tests 構造監査

基点 `64e3198`、現観測 `ae350d15145186ffbc9189ec500a6332f881d53b`。この間 internal/app の差分はなし。対象75 Go test files（約76との初期見積を実inventoryで訂正）。全ファイルのcase/helper/fixture、skip・環境補正・障害注入・期待結果構造を確認した。重点箇所は本文を読み、全assertionの意味検証・全テスト実行・全OSでの再現とは主張しない。production app の意味監査・config修正は別担当。監査中コード変更なし。

## 総合所見

新規の確定重大product bugは検出していない。test数の多さだけで受入範囲の完全性は証明できない。特に app単体 suite はCLI/MCPの結果写像を保証せず、coreの同じFold/TreeFilesをexpectedに使う比較は独立仕様の代替にならない。一方、実Git repository・raw foreign commits・remote hooks・別process CAS contender・ref不変性を観測する回帰は、実装の返り値を追認するだけのmockとは区別できる。

### T-01: fixture環境補正は必要だが環境別経路の証拠ではない

13 package の `main_test.go` は global/system Git config を os.DevNullへ向け、利用者の署名設定がfixtureに混入するのを防ぐ。サービスfixtureはローカル name/email と署名offを設定する。これは安定したテスト前提でありproduct fallbackではない。Git欠如はrequireGitのskip、SSH署名はssh-keygen不在/旧版でskipする。mandatory CIで実行されたことは別途tool capabilityとskip数で確認する必要がある。POSIX snippetテストだけはsh不在でskipするが、その外部契約はPOSIX用でありWindows native snippetの証明とはしない。

issueapp/issueapp_test.go:254–255 はos.DevNullでなく文字列 `/dev/null` を重ねて設定する。現在のWindows実行失敗を本監査では確認しておらず重大バグと扱わないが、portable fixture規則の統一候補。fixture補正削除には独立isolated config fixtureか外部環境に依存しない明示プロファイルが必要。

### T-02: 故障注入は実結果観測とseam依存を分ける

syncapp/local_cas_test.go:27–145 はnative test binaryをGit wrapperとして実行し、実Git別processでrefを進めて本物のCAS負けを作る。LookPathでwrapper選択を検証し、count fileとcontender ancestry・最終label/remote OIDを観測するので、注入が発火せず緑になる穴を閉じている。Windowsの.exeとcase-insensitive path、fixture後にoperation timeoutを作る補正は能力上必要。実の時計待ちだけでCASを再現するテストへの退行は避ける。

syncapp/retries_test.go:15–67 はpre-push hookがremoteを毎回更新し、traceの実push回数が retries+1 であることを確かめる。既存eventを異なる物理commitで繰返すfixtureはcanonical-copy冪等性に依存し、異内容同IDを作る偽造ではない。pre-receive恒久拒否では別refの成功も観測する。

wikiapp/write_test.go:330–358 の afterMerge seam はpush直前の別clone実書込みを同期する。`raced`発火を必須にし、最終remoteの3ページとcleanupを観測する。production seam自体の存在はrefactor候補だが、除去するなら同じ競合窓を外部hook等で確実に制御する代替が必要。純mockのCAS errorでは同じ証拠にならない。

fsckapp/compact_race_test.go:20–103 はstaleOIDを直接maybeCheckpointへ渡し、その後の実commitを親stateに含めるか検証する。thread raceのスケジューラ検証ではなく、観測時点ずれのdeterministicな反例である。issue/task status-from testsもparent eventを独立に辿り、記録fromがCAS採用親に一致するか確認する。

### T-03: 独立expectedの範囲

checkpoint/read/recompute比較は同じmaterialize.Registry.Fold/TreeFilesをreferenceに使うため、shortcutとfullfoldの差・cache偽造・保存treeとの差を検出する。両方で同じreducerバグがある場合は緑になるので、独立spec vectors/Pythonの代替ではない。entitychain/testTreeFilesはScratchまでstate.jsonにする専用rendererで、production feature layoutとの一致を主張しない（import cycle回避の限定fixture）。

一方、mode_integrityはGit tree modeを直接改変し、recomputeはblob内容だけの改竄とcheckpointstate偽造を別に作る。ref_contract testsはschema/context不正raw historyをfsck/verify/sync/initに通して、ref非進行まで見る。users_key_orderはfully signed invalid revokeが元Foldではignoredになること自体をassertし、admissionが拒否することを確認する。invalid fixtureを“正常化して通す”変更は診断境界を失う。

### T-04: skipと能力、時間・並行前提

固定clock/ULID fixtureを用いるclock_skew/order testsと、real wallclockを使う通常app testsが混在する。global testIDSeqはserialなcheckpoint fixture用であり、将来t.Parallelを追加するなら採番の所有と共有環境Setenvを見直す必要がある。現行でraceを実証したとの主張はしない。

native wrapper・Git hook・GIT_TRACEが実行できることはOS/ツール依存。traceは文字列parserであり、argv quotingやGit出力形式全般の証明ではない。batching testsはtrace resetでfixtureコマンドを除き、push --porcelainを識別するので、通常fixtureのpushが測定へ混入しにくい構造。trace回数の主張はperformance/transport契約の限定であって、data integrityは別assertionが必要。

## Coverage ledger

S=case/helper/期待結果構造確認。全75 filesをS確認。表の説明は保証範囲の要約であり、完全な仕様適合証明ではない。

| File | 構造・補償・独立性の確認内容 |
|---|---|
| `chatapp/chatapp_test.go` (125 lines / 3 cases) | S: 実Git fixture、thread lifecycle/list/filter/prefix/NotFound。service readbackを期待状態と比較。 |
| `chatapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `doctorapp/doctorapp_test.go` (208 lines / 9 cases) | S: isolated configと実Git。clock-skewは観測TSへclockを合わせwarning境界を検証。five checks件数は現契約への構造依存。 |
| `doctorapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `entitychain/checkpoint_test.go` (397 lines / 5 cases) | S: 専用state.json renderer、固定ULID採番、fullFoldとの差。legacy/merge/逆順/stale parentでfallback確認。 |
| `entitychain/checkpoint_trust_test.go` (134 lines / 2 cases) | S: 偽stateと古いsemantics/maxID改竄。read/write両方で不可信snapshot非採用、committed tree確認。 |
| `entitychain/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `entitychain/write_validation_test.go` (36 lines / 1 cases) | S: renderer spyの未到達とref不変、型付きinvalid-inputの三点確認。 |
| `fsckapp/checkpoint_read_test.go` (267 lines / 3 cases) | S: notify ack/users policyの非空fixtureを明示確認してからcompact比較。singleton再compact冪等性。 |
| `fsckapp/compact_race_test.go` (103 lines / 1 cases) | S: staleOID引数で時点ずれを再現、実parent/event count/read/次write/fullfold比較。 |
| `fsckapp/compact_size_test.go` (109 lines / 2 cases) | S: 巨大checkpoint拒否とref非進行/既存comments保持、foreignoversize raw commit診断。 |
| `fsckapp/email_ownership_test.go` (72 lines / 1 cases) | S: raw duplicate-email registryを生成、fsckとverify双方の診断。 |
| `fsckapp/fsckapp_test.go` (551 lines / 9 cases) | S: 実Git/raw corrupted envelope/月別shard/unknown ref/threshold/portable wiki。Fold比較は同reducer reference。 |
| `fsckapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `fsckapp/metaconfig_test.go` (93 lines / 1 cases) | S: raw tree/config差替えによりvalid/unsupported/missingを診断。 |
| `fsckapp/mode_integrity_test.go` (75 lines / 1 cases) | S: raw Git tree mode regular→symlink改竄、正常control後tree_mismatch確認。 |
| `fsckapp/recompute_test.go` (159 lines / 2 cases) | S: materialized blob改竄とcheckpoint state改竄を分離、正当head treeと診断対象を区別。 |
| `fsckapp/ref_contract_test.go` (248 lines / 3 cases) | S: malformed data/ref/entity raw履歴、compact非進行、sync/init非採用。unknownkind/status warningとの区別。 |
| `fsckapp/sign_test.go` (107 lines / 1 cases) | S: 実SSH能力probe/skip、compact commitの実Git署名検証。 |
| `initapp/initapp_test.go` (186 lines / 4 cases) | S: remote無し/不可達とidempotence、identity不足でrootを作らない。localconfig isolation。 |
| `initapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `initapp/stale_tracking_test.go` (61 lines / 1 cases) | S: remote削除済trackingをfixtureに残し、prune後の採用先を観測。 |
| `issueapp/clock_skew_test.go` (71 lines / 1 cases) | S: 固定ahead eventと後続comment拒否、clock_skew診断とref非進行。 |
| `issueapp/issueapp_test.go` (311 lines / 8 cases) | S: 通常service lifecycle/filter/prefix、identity不足、並行local writes。実repo state readback。 |
| `issueapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `issueapp/status_from_test.go` (103 lines / 2 cases) | S: 並行statusのcommit親をWalkChain+Foldしfromを比較、status実生成非ゼロ確認。 |
| `logapp/logapp_test.go` (242 lines / 5 cases) | S: feature横断actualhistory、actor/since/checkpoint除外。日付境界と並びは現log契約。 |
| `logapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `notifyapp/ack_ids_test.go` (62 lines / 1 cases) | S: ack対象prefix解決/曖昧/不在、書込前拒否と既存履歴保持。 |
| `notifyapp/auto_test.go` (190 lines / 5 cases) | S: self suppress/config disabled/mention resolution、実notification投影を確認。 |
| `notifyapp/group_test.go` (39 lines / 1 cases) | S: group展開と別email/unread実読取り。 |
| `notifyapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `notifyapp/notifyapp_test.go` (113 lines / 2 cases) | S: post/list/unreadとack冪等性。 |
| `notifyapp/write_validation_test.go` (40 lines / 1 cases) | S: undeliverable target/invalid sourceをappend前拒否しref保持。 |
| `snapshotread/snapshot_test.go` (17 lines / 1 cases) | S: rendered Markdown末尾改行bytes保持とinvalidUTF8拒否。完全履歴/権限検証の証拠ではない。 |
| `syncapp/batching_test.go` (368 lines / 5 cases) | S: 実Git trace reset、fetch/push集約/拒否refだけretry/結果順。trace parser依存は限定。 |
| `syncapp/clock_order_test.go` (100 lines / 1 cases) | S: 旧raw writerによるprecreate comment、元Fold消失をfixtureに保ちsync/ref/fsck/verify拒否。 |
| `syncapp/conflicting_ids_test.go` (253 lines / 4 cases) | S: sameID異内容を双方向とlinear/checkpoint経路で拒否、identicalcopy dedupe対照。 |
| `syncapp/discovery_test.go` (221 lines / 4 cases) | S: remote-only ref/kindfilter/tracking差/remote削除修復を実bareoriginで確認。 |
| `syncapp/fsck_after_merge_test.go` (68 lines / 1 cases) | S: 実event-union後fsck clean。mergeとfsckが共有するreducer外の意味誤りまでは証明しない。 |
| `syncapp/graft_test.go` (106 lines / 2 cases) | S: registry graftをFFと初回fetchで拒否、受入境界の別経路。 |
| `syncapp/local_cas_test.go` (314 lines / 2 cases) | S: native Git wrapper別process実CAS競合、注入count/ancestry/state/remoteとbounded exhaustion。lock timeoutはsetup後。 |
| `syncapp/main_test.go` (22 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。native CAS wrapper helperをtesting開始前にdispatch。 |
| `syncapp/meta_conflict_test.go` (142 lines / 4 cases) | S: 同版競合双方向/canonical等価/高版選択/両refとhistory保持。 |
| `syncapp/retries_test.go` (67 lines / 1 cases) | S: prepush hook実remote advance、tracepush数がretry+initial、budget0/1/5。 |
| `syncapp/sign_test.go` (141 lines / 1 cases) | S: 実SSH能力probe/skipとmerge commit署名の実Git検証。 |
| `syncapp/syncapp_test.go` (388 lines / 6 cases) | S: 実bareorigin2clone、shard/convergence/up-to-date、文字列分類unitとreal pre-receive拒否。 |
| `syncapp/trustroot_test.go` (274 lines / 6 cases) | S: 独立roots/pin local/remote/mismatch/sharedhistory、nonusers二root対照。 |
| `syncapp/unsafe_registry_test.go` (88 lines / 1 cases) | S: unsafe raw namesのread/write/sync非採用。 |
| `syncapp/users_key_order_test.go` (200 lines / 1 cases) | S: fullysigned raw revoke-beforeadd、valid repeatcopy対照、read/fsck/verify/sync/init非採用と他ref trust拒否。 |
| `syncapp/wiki_test.go` (257 lines / 6 cases) | S: 通常wiki FF/diverge/editing別扱い、meta human merge解決、schemaとsupportedfeature table。 |
| `taskapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `taskapp/status_from_test.go` (62 lines / 1 cases) | S: 実status eventのfromが親stateと一致、terminal後の規則も観測。 |
| `taskapp/taskapp_test.go` (193 lines / 4 cases) | S: 通常task start/done/filter/prefix/link、実service状態を期待と比較。 |
| `usersapp/agent_snippet_test.go` (43 lines / 1 cases) | S: POSIX sh capability skip、生成snippetを実行しshell substitution不発火/config literal一致。 |
| `usersapp/agent_test.go` (54 lines / 1 cases) | S: 実ssh-keygen能力skip、AddAgent生成key/registry/snippetの基本契約。 |
| `usersapp/email_ownership_test.go` (68 lines / 2 cases) | S: 異user email共有のref非進行、並行claimが一ownerのみ成功。 |
| `usersapp/keys_test.go` (150 lines / 8 cases) | S: valid/invalid/private/garbage、診断でmaterial非漏洩、comment違いadd拒否/revoke解決。 |
| `usersapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `usersapp/policy_test.go` (63 lines / 2 cases) | S: shape拒否でhead不変、schema許容extension保存。 |
| `usersapp/safety_test.go` (217 lines / 7 cases) | S: 既存human/key file保全、project別key、失敗時cleanup、preflight後collision、名前claim並行。OS permission検査はWindows除外。 |
| `usersapp/tree_safety_test.go` (57 lines / 1 cases) | S: unsafe namesをpureFoldで作りmemory storerにWriteTree、zero objectsを確認。disk/transport証明ではない。 |
| `usersapp/usersapp_test.go` (193 lines / 8 cases) | S: user/group/policy/key通常lifecycle、helper validation、stable name sort。 |
| `usersapp/write_validation_test.go` (23 lines / 1 cases) | S: unknown user kindをappendせずregistry未作成。 |
| `verifyapp/corruption_test.go` (141 lines / 3 cases) | S: raw envelope破損、他ref reports保持、corruptregistryの権限使用禁止、unknownref warning。 |
| `verifyapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `verifyapp/trustroot_test.go` (140 lines / 3 cases) | S: double-root/pinとgrafted root admin拒否、single-root正常対照。 |
| `verifyapp/verifyapp_test.go` (166 lines / 4 cases) | S: unsigned警告、missingref空report、複feature/shard包含。missingと環境failure区別は追加config修正側。 |
| `wikiapp/cleanup_safety_test.go` (66 lines / 1 cases) | S: manual worktree parent/siblingsを実ファイルで保全、normal/cleanup error条件。 |
| `wikiapp/clone_test.go` (116 lines / 2 cases) | S: plain cloneにwiki ref無しを先に確認、remote main/defaultbranch history祖先保持、no-syncはtrace接触無し。 |
| `wikiapp/divergence_test.go` (100 lines / 1 cases) | S: 実2clone divergenceとunchanged edit、conflict繰返し未解決非公開、merge後両ancestry保持。 |
| `wikiapp/main_test.go` (19 lines / 1 cases) | S: package TestMain: global/system Git configをos.DevNullへ隔離。 |
| `wikiapp/save_resume_test.go` (176 lines / 4 cases) | S: offline/rejectedpushのlocalref保持、worktree commit再開、permanent拒否push一回、nothing-to-save対照。 |
| `wikiapp/wikiapp_test.go` (163 lines / 4 cases) | S: raw wiki commitでshow/log、missingとnonrepo unexpectederror区別。 |
| `wikiapp/write_test.go` (593 lines / 11 cases) | S: 実bareorigin/worktree、validation保持、conflict解決、afterMerge race seam発火、cleanup、AST import規則とconflictmarker unit。 |

Coverage: 75/75 files。未確認範囲は全assertion意味・全OS実行・全fault組合せであり、未読ファイルを隠す意味の省略ではない。64e→ae350d1のapp差分0。後続config修正の新test filesはこのepoch外で、integration時に追加ledgerが必要。
