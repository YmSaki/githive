# core 補償コード監査

対象 epoch: `c5da0be02e87fa2e221662bea184e77787b1fa12`。追加差分: `64e3198`。コード変更ではなく設計判断用の読取り監査。基点は production 33 / test 41 = 74 Go files、追加 production 3 / test 2 = 合計79 files。production は関数の分岐・呼出し契約を意味的に確認した。tests はテストケース・期待結果・fixture/skip条件・対象不変条件の構造確認であり、全組合せを実行したとの主張ではない。core 外は呼出し根拠を限定参照した。外部URL・鍵・実環境識別子は記録しない。

権威: docs/02-data-model.md の immutable event/set fold と docs/03-sync-and-concurrency.md の CAS、docs/11-security.md の署名・主体結合、採用済 ADR-0012/0015/0016/0022/0026/0027。docs と spec の優先順位は別途文書統合対象であり、本報告では採用 ADR による限定例外を一般保証に拡大しない。

## 結論と根本不変条件

1. 参照を進める前に、対象履歴の完全性・論理イベント同一性・必要な依存順序が確定していること。CAS・shallow refusal・IDSet・key依存検査は分散／不可信入力の必須境界で、単純削除不可。
2. 検証で使う主体／鍵は一意の台帳主体と実署名に結合すること。曖昧 email の権限合成禁止、署名 principal の絞込み、失効時刻の保守的判定は維持する。
3. 未知／失敗／不存在は別状態であること。Git のエラーを空値へ変換する箇所と locale の偽固定に、現実の実装欠陥がある（下記）。
4. event ID の全順序と因果関係、汎用 JSON map と domain 型、台帳鍵文字列と鍵材料同一性を同一視した設計が、入力拒否・default・Scratch 復元などの重複を生んでいる。削除には上位の型／プロトコル変更が必要。

## 確定所見・判断

### C-01: locale 固定が実 subprocess では成立しない（確定バグ、修正を別提出）

場所: gitx/gitx.go:90–101、gitx/gitx_test.go:212–221。`LC_ALL=C` を継承環境の前に置くが、Go os/exec は重複環境変数の最後を採用する。継承 `LC_ALL=inherited` で同じ Cmd.Env を渡した実 subprocess の観測は `LC_ALL=inherited`。既存テストは先頭要素だけを確認し誤った仮説を固定する。

必要保証は gitx/gitx.go:453–458 の CAS 診断文字列判定および app push 失敗分類の locale 独立性。最初の不正 artifact は子プロセス環境。修復は末尾 override と実 subprocess regression。将来の第一原理案は subprocess adapter が Present/Missing/Conflict/Denied/Corrupt/Cancelled/Unknown を返すこと。ただし Git の終了コードだけでは全ケースを識別できないため、text adapter を直ちに全廃できない。

F=5 / I=6 / D=2 / Score=60。非C locale がインストールされている環境での全Git診断言語は未網羅。環境継承の反例自体は実測済。

### C-02: RevParse が全 ExitError を不存在へ変換（確定 adapter 契約違反、担当へ通知）

場所: gitx/gitx.go:190–203。missing ref は exit1、壊れた config・非repository 等は exit128 なのに全て empty,nil。Log:256 など呼出側が空履歴として扱う。MergeInProgress:755 も全 ExitError を false,nil。不存在保証は環境／破損の未知を含められない。config fail-closed 担当が実Git regression と修正を担当する。本監査では end-to-end の偽検証成功を再主張しない（入口ごとの repo preflight が異なる）。第一原理は typed result、当面は既知 missing exit だけ空値へ変換。元履歴の移行不要。受入は missing と bad config/nonrepo の分類が異なり、read/verify/sync が環境失敗を成功空状態にしないこと。

### C-03: Scratch 復元互換層は現行 production callgraph から外れている（除去候補）

場所: materialize/checkpoint.go:108–309、materialize/materialize.go:52、各 feature SetScratchDecoder 呼出し。`StateFromMap` の呼出しは core checkpoint tests のみ。entitychain checkpointBase は covered parent を full WalkChain/full Fold し、保存 state の canonical 内容と照合後、復元ではなく再構築 state を返す。StateToMap は checkpoint 発行／検査に現役なので削除対象外。

ScratchDecoder + typed map 復元の除去条件は internal 外を含む利用調査、受入の根拠を converter 自身の roundtrip から「不可信 checkpoint が full Fold と同じ結果か安全 fallback」に置き換えること。immutable event を書換える必要はない。将来 checkpoint を本当に高速化するなら source parent OID・covered ID set・reducer ruleset に結合した証拠を設計し直す必要がある。信用不能 snapshot を無検査採用する短絡は不可。

### C-04: map の dual state が補正層を増やす（設計由来、現在の誤結果と断定しない）

場所: materialize/materialize.go:19–36、issue.go:25–96,209–290、task.go:17–71、notify.go:17–35、checkpoint.go:19–106。Meta の表示配列と Scratch の typed set/history が並存する。JSON serialize/restore 時の型消失を更なる decoder が補う。missing field の default、asStringSlice の非文字列 skip、stringOr の空文字などは domain 型がないことへの対処でもある。

第一原理案: feature ごとの typed state を唯一の所有状態にし、Markdown/JSON を projection とする。除去前提は現在の deterministic bytes・legacy fold vectors・snapshot wire形状を維持する adapter、または明示 reducer/schema epoch の移行。既存署名 event や tree の silent regeneration による意味変更を禁止。受入は全順列/分割 merge の同一性と full fold/checkpoint equivalence。

### C-05: ULID の順序に因果を代用した結果の拒否層（契約選択由来、現行保護は必須）

場所: event/entity_order.go:15–52、event/users_key_order.go:17–67、idgen/idgen.go:42–86、chain/chain.go:328–378、merge/merge.go:21–58。reducer は precreate mutation / 未登録 user key を no-op にするので raw intake は順序不正を明示拒否し、app は clock_skew 拒否する。これはデータ損失に成功応答しないため必要。local counter は同時刻／clock rollback の衝突を抑えるが別machine因果の証拠ではない。

削除には新 version の causal event contract（例: predecessor/source token と total-order tie-break の分離）が必要。単なる時刻補正／既存 ID 書換えは署名と event 同一性を壊す。旧履歴は immutable に保ち、曖昧な失効意図には人間判断が必要。受入は ahead-clock peer の mutation、revoke-before-add、same ID different payload が成功消失しないこと。

### C-06: 公開鍵文字列の同一性と材料の同一性（contract gap を拒否で閉じる）

場所: event/users_key_order.go:22–64、materialize/users.go:95–153、sign/sign.go:42–59,197–241。legacy reducer は exact pub string を add/revoke のキーとする一方、signer は comment を落として fingerprint を材料で計算する。comment/spacing 別名で失効を潜り抜けないよう raw domain validator が曖昧な variants を拒否する。

第一原理は parsed key blob 由来の安定 KeyID と独立 comment metadata、revoke が KeyID を指定する契約。除去には旧文字列→KeyID の明示 adapter と raw invalid legacy の quarantine、旧署名の保全が必要。同一コピー／exact repeat revoke は idempotent のまま。共通 domain vectors がGo/Pythonで同じ拒否を返すことを受入にする。

### C-07: reducer の tolerant default と新 data schema（64e で境界改善、未知kindは未解決互換方針）

場所: materialize/materialize.go:67–99 と feature reducers、64e event/data_schema.go:34–91、refspace/event_context.go:15–26、materialize/status_audit.go:10–47。既知kindの malformed data を schema で拒否し、feature/entity mismatch を context validator で拒否、正しい形だが規則上 ignored の status を warning にする。reducer を変えずに誤黙認を診断する設計。unknown kind は future互換として warning、unknown field preserve と unknown operation の意味不明を同じ保証にしないこと。

新 numeric adapter は JSON.Number が schema library では string になる差を補い、integer exactness を float化前に確認する（data_schema.go:94–199）。schemaInstance は入力copyで既定値／原値の書換えなし。組込み schema・外部ref loader無しなので network fail-open はない。第一原理案は raw→ValidatedEvent の単一 admission pipeline、未知kind/古いversionを明示モードに分離。現時点で guards を除去して純 Fold の no-op を成功とみなす変更は不可。

### C-08: 署名結果の text adapter と conservative fallback（必要な防御）

場所: sign/sign.go:129–241。alias ごとに allowed_signers を絞ることで同じ user の複数 email を有効にし、複数 user が同 email を所有する場合は拒否する。principal の出力が読めない場合も allowed_signers を対象 email のみに限定する。fingerprint が読めない場合は全該当鍵が時刻上有効であることを要求し、曖昧性を成功拡大しない。旧署名のため revoked key を allowed_signers から削除しない。

除去には supported Git/OpenSSH adapter の structured verified signer evidence と revoke 規則に結合した互換試験が必要。自前の署名検証へ置換しない。受入は alias/ambiguous ownership/key rotation/unknown fingerprint の拒否境界を保つこと。read-only temp cleanup error の無視は authority を増やさないが残留ファイルの運用観測は別課題。

### C-09: 不可欠な分散／filesystem 補償

chain/lock.go:14–28 の process lock は同一 process の object/ref 操作直列化、chain/chain.go:582–588 の system Git CAS は別 process の ref 競合解決。どちらか一方の削除では同じ保証にならない。lock の Abs/EvalSymlinks fallback は path alias を扱う補正で、common-git-dir identity を typed repository handle に固定する案が本質的。

chain/shallow.go:22–63 は source branch shallow と project history shallow を区別し、後者のみ authority を拒否する。ReadTree は modeを落とすので tree integrity の最終証拠にはできず、fsck の Git tree OID照合が必要。

gitx/gitx.go:609–753 と worktree_ownership.go:15–63 は wiki未初期化時の deterministic synthetic root、remote cached branchへの接続、owned temp rootだけの削除を実装する。owner marker は不可信 config path から任意ディレクトリ削除に進まないため必須。将来は typed OwnedWorktreeHandle と idempotent cleanup outcome に置換できるが、user/manual worktreeを単に force delete してはならない。旧rootをunrelated mergeする手当は不可。

gitx/fetch_pruned.go は削除済remote refの tracking 残存を修正する。tracking namespace に remote identity がない問題の本質的解決は per-remote provenance であり、prune を削ることではない。

### C-10: 正当な wire 互換と診断用 fallback

chain/chain.go:241–258 は multiline legacy summary とイベント body を最後の blank-line+brace で分離し、write summary は正規化し roundtrip検証する。単純最初区切りへの退行は既存読取りを壊す。新formatでbodyを別object/length framedにするなら旧parserをread-only adapterとして保持する。event/canonical.go の Python-compatible number表記、strict.go の lone-surrogate 事前拒否、Extra preserve は言語間 canonical contract の実装であり、複雑さだけでは debt とみなさない。json.Numberをnative構築した不正literalへの低水準encode単体は弱いが、AppendEventは verifyRoundTrip で保存前拒否する（本監査で現在の保存 bypass は確認していない）。

## 実施と残余境界

実 subprocess の locale再現は実行した。他の pure/protocol動作は既存 suites と fixture assertions の読取りを基礎とし、監査中に全core fuzz／全OS／全localeを再実行したとの主張はしない。tests の optional git/ssh-keygen skip は fixture環境の機能不足で、実CI必須環境の代替証拠にはしない。独立した tests は不変条件を検証するものと converter / slice ordering の実装追認を区別した。C-01 の旧 test は後者の具体例である。

## Coverage ledger（基点74 files、未読productionなし）

P=全production分岐の意味読取り、T=ケース/期待不変条件/fixture/skip構造確認。Tは全行の独立形式証明ではない。

| File | Level | 確認内容 |
|---|---|---|
| `internal/core/chain/chain.go` | P | event write/roundtrip、DAG walk、ID/order admission、Git tree、CAS境界 |
| `internal/core/chain/chain_test.go` | T | AppendEventAndWalkChain, AdvanceRefCASRejectsStaleOld, WriteTreeDeterministic, OpenRepositoryMissingDir, WalkChainSinceEquivalence, WalkChainSinceMergePreservesNewerBranch, BenchmarkWalkChainSinceVsWalkChain; optional tool/symlink前提あり |
| `internal/core/chain/checkpoint.go` | P | checkpoint parent/coverageとScratch serialization/typed restore（packageごと本文参照） |
| `internal/core/chain/lock.go` | P | repository path正規化とprocess排他 |
| `internal/core/chain/main_test.go` | T | Main |
| `internal/core/chain/message_test.go` | T | AppendEventSummaryRoundTrip, ExtractEnvelopeReadsLegacyMultilineSummary, ExtractEnvelopeMergeCommitHasNoEvent, ExtractEnvelopeRejectsMissingEventOnNonMerge |
| `internal/core/chain/roots.go` | P | physical root探索、caller shallow前提 |
| `internal/core/chain/shallow.go` | P | reachable boundaryとsource/project区別 |
| `internal/core/chain/sign_test.go` | T | AppendEventSignatureLeavesTreeUnchanged, AppendEventNilSignerIsUnsigned, AppendEventSignerErrorAborts, AppendMergeSigns |
| `internal/core/chain/tree_test.go` | T | WriteTreeRejectsInvalidPathsBeforeWritingObjects, WriteTreeRejectsFileDirectoryCollisionsDeterministically, WriteTreeMatchesGitTreeOrdering |
| `internal/core/event/canonical.go` | P | pretty/compact canonical wire、Python numeric、Unicode ordering |
| `internal/core/event/canonical_test.go` | T | CanonicalVectors |
| `internal/core/event/compact_test.go` | T | EncodeCompactLineKeyOrderAndEscaping, EncodeCompactLineEmptyContainers, EncodeCompactLineNoTrailingNewline, EncodeCompactLineNumberRules |
| `internal/core/event/compact_vectors_test.go` | T | CompactVectors |
| `internal/core/event/entity_order.go` | P | sole createとprecreate操作拒否 |
| `internal/core/event/entity_order_test.go` | T | ValidateEntityOrderRejectsSilentLossAndTakeover |
| `internal/core/event/envelope.go` | P | version/size/actor/TSとExtra保持 |
| `internal/core/event/envelope_test.go` | T | EnvelopeVectorsValidate, ValidateRejectsBadEnvelopes, CanonicalJSONRoundTrip, OrderingVectors |
| `internal/core/event/order.go` | P | ID total order、入力slice非破壊 |
| `internal/core/event/schema_version_test.go` | T | DecodeAcceptsKnownOlderVersions, DecodeRejectsUnknownNewerVersion, DecodeRejectsMissingData |
| `internal/core/event/strict.go` | P | duplicate keys/depth/UTF8/lone surrogate拒否 |
| `internal/core/event/strict_test.go` | T | StrictJSONSharedVectors, CanonicalEncodingRejectsInvalidStringsAndExcessiveDepth, EventJSONSizeBoundaryIncludesCanonicalBytes, FromMapRejectsFractionalVersion |
| `internal/core/event/unique.go` | P | canonical payload同一性と同ID衝突 |
| `internal/core/event/unique_test.go` | T | EventIdentityVectors |
| `internal/core/event/users_key_order.go` | P | user/key依存、exact pub variants、idempotence |
| `internal/core/event/users_key_order_test.go` | T | UsersKeyOrderDomainVectors |
| `internal/core/event/write_data.go` | P | new write shape/enum/default境界 |
| `internal/core/event/write_registry_notify.go` | P | users/notify write name/target/source境界 |
| `internal/core/event/write_registry_notify_test.go` | T | RegistryAndNotifyWriteBoundaries |
| `internal/core/gitx/config_bool.go` | P | Git bool解釈とmissingのみdefault |
| `internal/core/gitx/config_bool_test.go` | T | ConfigBoolUsesGitParserAndMissingDefault |
| `internal/core/gitx/fetch_pruned.go` | P | prune選択とremote option拒否 |
| `internal/core/gitx/fetch_pruned_test.go` | T | FetchModesRejectOptionRemote; optional tool/symlink前提あり |
| `internal/core/gitx/gitx.go` | P | subprocess結果分類、remote/refs、push partial結果、wiki lifecycle |
| `internal/core/gitx/gitx_test.go` | T | CheckVersion, ParseVersion, ForEachRefAndRevParse, IsRefUpdateRetryable, UpdateRefBadRefNameIsNotCASMismatch, DeleteRefBadOldOIDIsCASMismatch, GitEnvForcesLCAllC, UpdateRefCAS, ParsePushPorcelainFastForward, ParsePushPorcelainVariants, VerifyCommitUnsigned, FetchAndPush, Show, ShowOtherGitErrorIsNotErrNotFound, IsShowNotFound, Log; optional tool/symlink前提あり |
| `internal/core/gitx/main_test.go` | T | Main |
| `internal/core/gitx/remote_test.go` | T | ValidateRemote, FetchPushLsRemoteRejectOptionRemote; optional tool/symlink前提あり |
| `internal/core/gitx/wiki_base_test.go` | T | WorktreeAddUsesCachedWikiWithoutLocalRef, WorktreeAddIndependentWikiRootsAreIdentical |
| `internal/core/gitx/worktree_ownership.go` | P | owner marker/repo identity/symlink cleanup境界 |
| `internal/core/gitx/worktree_ownership_test.go` | T | WorktreeRemovePreservesUnmanagedPaths, WorktreeRemoveRequiresMatchingRepositoryOwner, WorktreeRemovePreservesUnmarkedAndSymlinkPaths; optional tool/symlink前提あり |
| `internal/core/gitx/worktree_test.go` | T | WorktreeAddFirstSaveCommitRemove, WorktreeAddAtRef, MergeWikiRemoteClean, MergeWikiRemoteConflictAndCleanup |
| `internal/core/identity/identity.go` | P | email必須、display name fallback、wallclock |
| `internal/core/idgen/idgen.go` | P | mutex付き80-bit increment、rollback、overflow拒否 |
| `internal/core/idgen/idgen_test.go` | T | GeneratorNewWithTimestampClockRollback, GeneratorNewWithTimestampConcurrent, GeneratorNewWithTimestampCounterOverflow |
| `internal/core/materialize/chat.go` | P | create/post/edit/supersedesとparticipants二重状態 |
| `internal/core/materialize/chat_test.go` | T | ChatCreateWithBodyBecomesFirstMessage, ChatPostAndParticipants, ChatPostSupersedes, ChatEditMetaAndArchive, ChatSecondCreateIgnored, ChatCheckpointIgnored, ChatFoldOrderInvariance |
| `internal/core/materialize/checkpoint.go` | P | checkpoint parent/coverageとScratch serialization/typed restore（packageごと本文参照） |
| `internal/core/materialize/checkpoint_fixtures_test.go` | T | 共通fixture / checkpoint event set と期待stateの組立て |
| `internal/core/materialize/checkpoint_test.go` | T | CheckpointShortcutMatchesFullFold, CheckpointRoundTripPreservesScratch, StateFromMapRejectsOldFormat, StateFromMapRejectsUnknownScratchKey, FoldFromNilIsFold |
| `internal/core/materialize/email_ownership.go` | P | email owner一意性、alias重複除去 |
| `internal/core/materialize/email_ownership_test.go` | T | EmailOwnershipAllowsAliasesButNeverCombinesUsers |
| `internal/core/materialize/issue.go` | P | labels/assignees/links、default、status no-op |
| `internal/core/materialize/materialize.go` | P | 汎用State、unknown-kind ignore、FoldFrom前提 |
| `internal/core/materialize/materialize_test.go` | T | FoldOrderInvariance, FoldCheckpointTransparency, FoldIssueVectors, FoldTaskVectors, FoldChatVectors, FoldNotifyVectors, FoldUsersVectors |
| `internal/core/materialize/notify.go` | P | ack dedupe、月別projection、source/target保持 |
| `internal/core/materialize/notify_test.go` | T | NotifyPostBuckets, NotifyAckDedupeAndSort, NotifyCheckpointIgnored, NotifyFoldOrderInvariance |
| `internal/core/materialize/task.go` | P | owner default、terminal transition、履歴Scratch |
| `internal/core/materialize/task_transition_test.go` | T | TaskCancelFromTerminalIsIgnored, TaskCancelFromNonTerminalStillWorks, TaskResumeFromTerminalStillWorks |
| `internal/core/materialize/users.go` | P | pub exact identity、policy replace、email authority |
| `internal/core/materialize/users_test.go` | T | UsersUserSetCreatesAndUpdates, UsersKeyAddAndRevoke, UsersKeyOpsIgnoredForUnknownUser, UsersGroupSetAndRemove, UsersPolicySetIsWholeObjectReplace, UsersIsAdmin, UsersCheckpointIgnored, UsersFoldOrderInvariance |
| `internal/core/merge/entity_order_test.go` | T | UnionRejectsUnsafeEntityOrderAcrossSeparateHistories |
| `internal/core/merge/merge.go` | P | canonical set unionとorder検査、収束 |
| `internal/core/merge/merge_test.go` | T | MergeCommutative, MergeAssociative, MergeIdempotent, MergeConvergesAcrossSplits, MergeConvergesFromSharedAncestor, MergeRejectsConflictingEventIdentitiesInBothDirections |
| `internal/core/refspace/refspace.go` | P | namespace parse、future shardとtracking inverse |
| `internal/core/refspace/refspace_test.go` | T | EntityRef, ParseSingletons, ParseEntityRef, ParseRejectsInvalid, FilterByFeature, ParseNotifyShardAccepted, ParseNotifyShardRejectsMalformed, FilterByFeatureExcludesNotifyShards, RemoteTrackingRef, LocalRefFromTrackingIsInverse |
| `internal/core/sign/attach.go` | P | signing config failclosed、key::/tilde/literal adapter |
| `internal/core/sign/attach_test.go` | T | ResolveSignerOffWhenGpgsignUnset, ResolveSignerOffWhenGpgsignFalse, ResolveSignerRejectsNonSSHFormat, ResolveSignerRequiresSigningKey, ResolveSignerProducesVerifiableSignature, SignBufferHonoursExplicitProgram, SignBufferReportsProgramFailure, ResolveKeyFileLiteralPublicKey, ResolveKeyFilePathIsLeftAlone, ExpandHome, ResolveSignerUsesGitBooleanSemantics; optional tool/symlink前提あり |
| `internal/core/sign/email_ownership_test.go` | T | VerifyCommitRejectsKeyFromAnotherUserSharingAdminEmail, VerifyCommitAcceptsSameUserEmailAlias |
| `internal/core/sign/main_test.go` | T | Main |
| `internal/core/sign/sign.go` | P | allowed_signers sanitization、principal/fingerprint/revocation fallback |
| `internal/core/sign/sign_test.go` | T | BuildAllowedSignersDropsCommentIncludesRevoked, BuildAllowedSignersSkipsUnsafeLines, FirstTwoFields, ParsePrincipal, SigningKeyValidAt, FingerprintMatchesSSHKeygen, WriteAllowedSignersTempFileRoundTrip |
| `internal/core/sign/verify_rotation_test.go` | T | VerifyCommitRevokedKeyAfterRotation |
| `internal/core/wikifs/wikifs.go` | P | portable path/case/asset size shared純粋検査 |
| `internal/core/wikifs/wikifs_test.go` | T | CheckCleanTree, CheckReservedNames, CheckTrailingDotSpace, CheckInvalidChars, CheckCaseCollision, CheckOversizeAsset, ViolationPathPopulated |

## 64e3198 差分 ledger

| File | 確認 |
|---|---|
| chain/chain.go | EventJSONとparserでeventBodyFromMessage共有、監査対象bytesの分離規則一致。旧読取り規則変更なし |
| event/data_schema.go | P: embed/resolve once、known/unknown判定、exact integer先検査、copyしてschema adapter、network loader無し |
| event/data_schema_test.go | T: extensions、unknown kind、malformed types、全positive fold vectors、巨大exponent/cycle拒否 |
| materialize/status_audit.go | P: reducer transition predicate再利用、sorted ID dedupe、実foldを進めて履歴診断、結果stateは変更しない |
| materialize/status_audit_test.go | T: issue/task ignoredとaccepted status、physical duplicateをwarning重複にしない |
| refspace/event_context.go | P: wiki例外、feature prefix、entity ID一致。nil処理はcaller envelope検査前提 |

追加5 filesの確認済、合計79 files。core外のschema wiring/full read/sync/verify/fsck/init adoptionは他担当の監査範囲。新関数を定義しただけでは全入口保証としない。
