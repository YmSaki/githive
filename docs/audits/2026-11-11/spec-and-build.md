# spec 実装と Taskfile 構造・意味監査

対象 epoch `1db77fc`（依頼時固定）。読取り対象は spec 配下 `.py` 10 files + `.go` 1 file、Taskfile.yml 1 file = **12/12 files**。JSON vectors自体の全件再検査は対象外。Go/Git/test subprocessは実行していない。環境のprocess/thread資源不足により最初のrg inventoryがthread作成に失敗したため、その後は単一Pythonによる列挙と静的読取りを使用した。実行結果の新規PASSを主張しない。

## 意味的な結論

参照実装のpure foldと、外部履歴を受理するadmissionを区別する必要がある。Python foldはGo同様にmissing create/unknown kind/invalid transitionを無視し、一部fieldをdefault化する。これは既存fold意味論の再現であって、その入力が保存・認証可能である証拠ではない。新rawusers domain validatorはこの区別を維持し、旧foldを改変せずignored revokeを入口で拒否する。

`fold_chat.py`、`fold_notify.py`、`fold_users.py`、compact canonical encoderはGoの逐行移植を明記する。同じ考え違いを両言語へ移せるので、二言語一致を独立要件の証明とみなさない。checked-in expected vectorsは実行時にGo出力を呼んで生成しておらず、実装追認から一定の分離があるが、expected作成時の独立性はこのsource読取りでは証明できない。旧comment「Goが正」とdocsの規範優先の整合はarchitecture側の既知文書closure対象。

canonical pretty/compactの共通scalar規則、strict Unicode/depth検査、Go/Pythonで異なるwhitespace定義の明示補正は必要な言語境界である。単なる行数削減のため片方を削ると参照比較が失われる。除去するなら独立した規範アルゴリズムとcross-language境界vectorsが必要。

## 特記

### S-01 defaults と compat の適用範囲

issue/task/chatは最初のcreateだけ採用し、precreateを無視、unknownkindとcheckpointはno-op。issueはlabels/assigneesをset化、taskはowner空値をactorへ、usersはkind/status/空配列を初期値にする。notifyはtitle/body/sourceの扱いにPython `.get` とGo型assertionの差があり、malformed dataまで完全同値とは限らない。schema-validな入力集合とpure legacy-fold vectorsのどちらを比較するかを固定する必要がある。

users.domain_validationはID衝突を先に確認し、user_set→key_add→exact-pub revokeの順を拒否で閉じる。`_KEY_FIELDS`はPython splitがGoより多く認識する制御文字を除く明示的な互換adapter。これはデータを書換える正規化ではない。KeyIDへ変更する新契約が成立するまで現拒否を削除しない。

### S-02 validator は全production intakeの代替ではない

validate.pyはenvelope+knownkind schema、canonical expected、fold expected、固定seedの3shuffle、ordering、strict decoding、ID identity、users domain vectorsを別に検査する。positive schemaだけでなくnegativeを持つ点は有効。fold/ordering/canonical等はmissing folderを明示FAILにする。一方event-identityのglobには空集合ガードがなく、当該vectors欠落だけでは独立したFAILを増やさない。これはcoverage上の局所穴であり、現epochのvectorsが消失していると確認したものではない。

fold比較の `zip(fields, expected, got)` はtuple arity自体をassertしない。現在の各foldは所定tupleを返すが、将来返り値欠落を検知する枠組みとして弱い。reference正当性の追加条件候補であり、現在のproduct重大バグとは扱わない。

strict rawテストだけdecode_strictを使い、通常vectorファイルはstdlib json.loadで読む。そのため通常vectorのduplicate keysやfloat roundingをstrict reader検査したことにはならない。Python stdlibでoverflowするexponentや巨大整数の環境差まで、全production受入と同じだと拡大しない。config exact integer判定は新projectconfig testsの別境界である。

### S-03 例外集計と期待値の独立性

canonical/fold/orderは例外をcase FAILへ変換するので、例外握り潰しによるPASSではない。fixture load/依存jsonschema欠如の失敗は終了異常となる。schemaは入力を書換えるdefaultsを注入せず検査する。shuffle3回は決定的な限定samplingであり全順列証明ではない。merging/署名/Git tree mode/GC到達性はこのPython checkerの対象外。

### B-01 Taskfile は便利なrunbook、全受入を自動証明しない

build/test/vet/fmt/lint/spec:validateは各ツールの非zeroを失敗として返す。`ci`はlint→test→specでありbuild:smokeやartifact stdio/MCP/3OS QAは含まない。`ci` descのCI-equivalentを全CI jobとの完全同一と読まない。fmtのshell test/substitution、canonical lintのbash、cleanのrmはPOSIX shell/tool依存で、platformsにwindowsがあるだけでnative Windows toolchain完全対応を証明しない。既存CI実績は別証拠である。

cleanは固定binとPython cacheだけを削除する開発補助操作。可変user pathを削除するfallbackはない。canonical import lintは別scripts監査の既知parser限界があり、canonical wire correctnessの唯一の証拠にしない。spec:validateはjsonschema installを前提とし、Taskfile自身が依存版をpin/導入するわけではない。

## File coverage ledger

| File | 静的読取りの意味・構造確認 |
|---|---|
| spec/reference/canonical_json.py (219 lines) | pretty/compact共通scalar、Python repr numeric、bool-before-int、UTF8/depth前検査。compact逐行移植の独立性限界。 |
| spec/reference/domain_validation.py (41) | users key因果・exactpub variants・ID collision first、Go whitespace差adapter、immutable拒否。 |
| spec/reference/events_common.py (53) | ID sort、全canonical envelope同一性、same-copy順維持。ULID regexはoverflow upper bound未表現の既知profile。 |
| spec/reference/fold_chat.py (139) | firstcreate、participants set/derivedarray、supersedes existingtarget、defaultbody、unknown/checkpoint no-op。 |
| spec/reference/fold_issue.py (202) | transitions、set labels/assignees、supersedes、links、updated/closed timestamps、precreate/default。 |
| spec/reference/fold_notify.py (104) | singleton lazy meta、month derivation、post fields、ack actors dedupe/sort、unknown no-op。 |
| spec/reference/fold_task.py (184) | owner default、terminal cancel/reopen規則、history/notes、links、最初create、checkpoint no-op。 |
| spec/reference/fold_users.py (145) | default identity、protected keys/username、literalpub add/revoke、group/policy whole replace、unknown no-op。 |
| spec/reference/strict_json.py (61) | UTF8、lone surrogate、depth prepass、duplicate key、nonstandard constants、stdlib decoderとの分担。 |
| spec/schemas/embed.go (9) | 全*.jsonをbuildへembedする読取り専用FS。同一schema sourceをGoに配布、ファイル選別・runtime fetchなし。 |
| spec/validate.py (507) | positive/negative schema、canonical/fullcompact、5fold expected/shuffle、ordering、strict、ID/domain、集計exit。個別coverage穴はS02。 |
| Taskfile.yml (64) | build/test/vet/fmt/canonical lint/spec集約、ci依存範囲、shell前提、固定path cleanとsmokeの分離。 |

## core差分の追補（別担当精読を参照）

依頼により次の3filesはroot/securityの精読・既存試験証拠を参照する。ここで新たに全意味検証/実行したとの主張はしない。

| File | 差分と証拠の所有 |
|---|---|
| internal/core/projectconfig/version.go | present-invalidとday0 absentの分離、exact integer／未来版。root + security精読、ADR0032/Issue191。 |
| internal/core/projectconfig/version_test.go | exact整数・丸め小数・巨大exponentのtable、外部mockなし。担当test PASS報告。 |
| internal/core/gitx/revision_errors_test.go | realGit missing exit1とnonrepo/brokenconfig128を分離、RevParse/MergeInProgressのfailure伝播。config担当修正回帰。 |

app-tests側の75file基点は不変。後続追加 `cmd/githive/config_guard_test.go` はboundary担当、`projectconfig/version_test.go` はcore追補であり、app75へ重複加算しない。

## 残coverageと結論

対象12filesの静的確認完了。JSON全vectorsの内容、全Python数値領域、全OS shell、依存library版差、実配布物QAは未実行/対象外。重大確定の追加実装修正は提案しない。必要な境界を保ちながら、規範優先の明文化・arity/empty-set guard・期待値生成の独立性を将来の検証改善候補とする。
