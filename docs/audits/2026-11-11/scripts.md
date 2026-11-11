# Scripts / CI 補償と検証責務の監査 — Proposal / 未採用

対象 epoch: `64e3198e28cbf38d9e234fda301e0f5c9af612f0`。実装変更・公開操作なし。監査所見と、Phase 6 の検証境界の提案を分ける。CI の成功や現在の証拠を取り消す文書ではなく、何を証明しているかを限定し、補償を除去する前提を定義する。architecture.md と同じ CON-01〜05 を上位制約とする。

## 1. 全対象 inventory と責務

以下は scripts3本、workflow3本、hooks3本、Taskfile、hook登録の11ファイルを全文読取りした範囲。根拠となる docs/14、spec/README、CLAUDE.md と release-check skill は関連節を参照した。Goの全テストは他の分担ledger、spec/referenceの全実装はcore/app担当の範囲であり、本稿はその全行再監査を主張しない。

| ファイル | 現在の責務・観測 | 判断 |
|---|---|---|
| `scripts/acceptance.py` | 既存binaryをbuild/installせず実行。新規出力dir、隔離Gitconfig、timeout、stdout/exit/JSON、MCP lifecycle、正常tool inventory、resource、署名、実行前後binary hashとsource補助metadataを記録。 | 配布物境界として必要。正常呼び出しcoverageと意味論postconditionのcoverageを分ける。 |
| `scripts/source-install-smoke.py` | checkoutを新しいGOBINにgo installしhelpとGo metadataを確認。旧ファイル上書きやglobal installをしない。 | CLI buildと実installの差を検証する独立責務。acceptanceと一本化して検証対象binaryを無言置換しない。 |
| `scripts/check-canonical-json.sh` | internal/cmdの`"encoding/json"`文字列をgrepしevent外production importを拒否。 | 境界ポリシーを文字列で代用。raw-string importを見落とす反例あり。AST/typeベースへ移す候補。 |
| `Taskfile.yml` | build/test/fmt/vet/lint/spec/clean、local CI相当のtask。 | developer入口。CIと同じ保証を別の呼び方で提供すること自体は不正な補償ではない。 |
| `.github/workflows/ci.yml` | Ubuntu/macOS/Windowsのvet/build/test/source-install、非Windowsのgofmt/importlint、別Ubuntu spec job。 | 実OSのadapter境界は必要。job定義の存在はそのepochのjob実行成功ではない。 |
| `.github/workflows/auto-pr-review.yml` | 非draftだけAIレビュー、古い実行cancel、規約・spec・scope確認、後続Issueの補助。 | process lane。draftのskipはreview承認でもfailでもない。AIコメントをnormative contractや人間承認に代用しない。 |
| `.github/workflows/claude-issue-triage.yml` | human event/triage label、Bot loop防止、情報・scope・重複・テスト観点の補助。 | process lane。triage:readyは実装の妥当性・release合格ではない。 |
| `.claude/hooks/pre-push-gate.sh` | Bash tool文字列に`git push`を含むとGo test+specを実行。hook JSON失敗/対象外commandは通す。 | developer feedback。すべてのpushを強制するセキュリティ境界ではない。 |
| `.claude/hooks/post-edit-go.sh` | Edit/Writeの.goにgofmt、importlint。gofmt不在はwarning。 | 編集補助。実際の最終tree/CI検証の代替ではない。 |
| `.claude/hooks/lib.sh` | repo rootとPython/jsonschema interpreterを選ぶ。Windows alias等に対応。 | tool環境差の必要なadapter。存在しない依存を「テスト成功」にしないfallbackであることが条件。 |
| `.claude/settings.json` | 上記hooksのtool matcher登録とplugins設定。 | 該当clientの実行経路だけに有効。別client/直接shell/APIを制御するものではない。 |

## 2. 確定した境界ポリシーの反例

### UCA-SCR-01: import policy lint が妥当なGo importを見落とす（PROCESS_DRIFT / medium）

Contract A: CLAUDE.md / docs/02 / canonical import ruleは、productionのevent外にencoding/jsonを直接importしないこと。Contract B: scriptはdouble-quotedの文字列一致だけでimportを認識する。

最小反例（production変更なし、使い捨てfixture）:

```go
package example
import `encoding/json`
var _ = json.Marshal
```

コピーした現scriptをこの`internal/example/raw.go`へ実行するとexit0・`PASS: encoding/json usage is confined...`。Go parserはImportPathを`encoding/json`と解釈し、実際にGo compilerでpackageをbuildできた。実行したfixtureにはテストがないため、`go test`のno test filesを製品テストPASSとは数えない。再現は監査用の隔離fixtureで記録した。公開文書には上の最小sourceとscript結果だけを転記すれば足り、生成fixtureや実行ファイルを同梱する必要はない。production repo/sourceは変更していない。

Closure: 新しいsource→文字列grep→違反なし→CI green。import構文の別表現が未接続。最初の不正artifactはlintの認識器で、canonical contractを緩める必要はない。現productionにこのimport違反が存在するとは主張しない。

必要解決: 同じポリシーをGo AST/import path正規化で検査するか、compile-timeの依存境界へ移す。除去前にdouble/raw/alias/grouped importと許可test/event packageの反例を確認する。scriptの削除だけは解決にならない。

発生頻度: 2
影響度: 3
発生手順の難易度: 2
Score: 12

## 3. 過大解釈してはいけない現行の証拠

FACT: acceptanceは全登録toolが正常responseを返したことと、選択したroundtrip等を実際に確認している。`tools_called`はtool成功後に追加され、登録一覧との差集合も検査する。これは有用な入口coverageである。一方、以下は別の保証であり、未実行を失敗0に読み替えない。

| 事実 | より強い主張へ昇格できない理由 | 追加の実証対象 |
|---|---|---|
| 正常な全tool呼び出し | call成功と各commandの効果は同一ではない。例えばchat_archive後のchat_showは呼ぶがarchived値をassertしない。no-op実装でもこの局所観測は変わらないという静的反例がある（mutant全体実行はしていない）。 | command毎の最小postconditionとcross-transport再読込。 |
| CLI/MCPの多くを`--no-sync`で実行し最後にexplicit sync | local-firstとexplicit配送の正常経路には有用だがdefault auto-syncの成功warning・採用後の到達性を直接証明しない。 | 保存成功→auto-sync成功/失敗/設定採用のreceipt。boundary監査の#189反例をnegative obligationにする。 |
| fsck --compactを一度呼ぶ | チェーンが閾値に達していなければcheckpoint生成・fallbackの証拠にならない。 | 閾値超過、stale/偽checkpoint、fullfold同一性。既存単体/結合テストの証拠を別に接続する。 |
| binary hash前後一致 | 同じbinaryを試験した証拠。optional source SHAがそのbinaryを生成した証拠ではない（script自体がlimitsに明記）。 | 固定module version/build入力、binary digest、builder実行結果の結合。 |
| source-installのhelp/metadata成功 | 実install/entrypointの証拠であって、full機能・署名・配送の実動ではない。 | インストールされたそのbinaryをacceptanceへ渡し、前後hashを束縛する。 |
| Windows Go tests成功、あるいはLinux acceptance成功 | 他のOSのeditor/helper/SSH/実Git動作の証拠ではない。POSIX以外のpolicy editorfixtureは現在limitsへskipを記録する。 | 必須profileのOS capability実行、任意項目のNOT_CHECKED分離。 |
| local hookとCIが同じtestを実行 | hookは文字列matcher・client選択・environmentに依存し、任意pushの完全強制ではない。 | authoritativenessはfinaltree+required CIとreviewに置く。hookはfast feedbackと明記。 |
| AI review/triageの成功やskip | 意味論の実証や人間のmerge承認ではない。 | contract根拠、実行証拠、人間の承認を別のstatusで記録。 |

これらは現scriptの明示した限定scopeと整合するものを含む。限定scopeの正常PASSそのものをfalse-PASSと呼ばない。release全体の判断へ再利用するときに、省略した依存・義務がopenになるという監査結果である。

## 4. 第一原理の検証アーキテクチャ案

PROPOSAL SCR-ARC-01: verificationを「何コマンド実行したか」でなく、契約に対する次の結合として扱う。

```text
Acceptance obligation
  → actor/profile/required capability
  → isolated fixture + fault/command scenario
  → independent expected observable
  → binary/build/contract/runtime subject
  → per-obligation result
  → scope-aware aggregate + review/publication gate
```

現在のPython harnessを捨てる提案ではない。binary installとinvokeを分けた現構造は保持する。共通化するのはprocess runnerのtimeout/UTF8/output/path管理、semantic outcomeの記録、provenance schema、obligation inventoryであり、異なる役割の試験を一つの自己確認oracleへ統合しない。

### 必須の結果状態とsubject

- per obligation: PASS / FAIL / NOT_CHECKED / NOT_APPLICABLE。中断は失敗対象と未実行対象を別に残す。単なるskipやselected subsetをfull PASSへ昇格しない。
- subject: binary digest、source commitとdirty/generated inputs、module/dependency lock、build/toolchain/options、contract/schema/vector/runner revision、OS/arch、Git/SSH adapter version、profileと主要環境前提、実行したobligation ID。判定に無関係とする項目は理由を記録する。
- artifact hashだけで信頼を作らない。誰のbuilder/evaluatorがどの入力から生成し、そのreportがどう保持・検証されたかを結ぶ。自己申告sourceをbuild attestationと呼ばない。
- freshness: subject又はrequired obligationが変わればその保証を再検証する。docs-only変更が無関係という場合も、実行runner・contract解釈・生成入力を変えていないという範囲を明記する。
- public summaryにはsubject/result/limitsだけを許可し、raw fixtureの秘密鍵・環境パス・実環境URLを公開しない。raw証拠は必要な場所に限定保存する。

### 補償を減らす際の置換条件

| 除去候補 | 先に置く代替 | 削除しても変えてはいけない保証 |
|---|---|---|
| shell文字列grep importlint | Go AST/import graphのportable policy check | event外canonical codec禁止。Windowsでも同じpolicyを検査する。 |
| localhookとCIの手動command列重複 | authoritativeな一つのverification manifestと各環境runner | 独立したlocal feedback/required CIの2段階は残す。CI未実行をlocalPASSで代用しない。 |
| source/binary SHAを手で報告書へコピー | install/build receiptをacceptance subjectへ直接渡す | 実際に試験したbinaryと固定したsource/build入力の結合。 |
| scriptごとの配列/JSON整形・timeout補助 | process runnerとresult codecの小さな共通adapter | UTF8/バイナリ区別、終了コード、stdout/stderrの独立、timeout診断とデータ保持。 |
| 全tool呼び出し後の事後チェックリスト修正 | tool/command registryと独立obligation IDの対応表 | 登録漏れを検出しつつ、実装からexpectedを自動生成して自己PASSにしない。 |
| testのplatform別shell workaround | nativeなfault injection adapter又は明示capability fixture | 故障が本当に注入された証拠、CAS競合・Windows動作の実実行。skipしてgreenにしない。 |
| 手作業のold/newQA説明分岐 | immutable run recordとscope/freshness判定 | 古いSHAのPASSと新SHAのNOT_CHECKEDを区別。履歴報告を上書きしない。 |

Go/Pythonの二重参照は、相互独立性と契約ベクタという価値がある。片方からもう片方を全生成して「二実装一致」を独立検証と呼ぶ案は採らない。schemaからvalidator/typeを生成するなら、独立な正負例と意味論の判定器を残す。

## 5. immutable証拠・履歴の移行

検証記録も追記のrun単位にする。旧25メッセージ・新全41tool・その後の新tool追加は異なるscope/versionとして保持する。新result schemaへ移すときは旧recordの不足字段を推測でPASSへ補完せず、legacy imported / unknownを表す。

プロジェクトイベント履歴の移行はarchitecture.mdへ委ねる。scripts共通化で旧Git履歴を再生成・resignしたり、テスト用cleanupを製品のuninstallへ混ぜたりしない。インストール検証が新しいテストbinaryを作る場合と、指定binaryを試す場合の入口は分けたままにする。

## 6. 提案の受け入れ条件

1. raw/alias/grouped import反例でpolicy違反を検出し、event/testの正当な例は拒否しない。
2. archive/link/ack/notify等をno-opにしたmutantを正常responseだけで合格にしない。各obligationに独立な再読込/履歴/配送のobservableが存在する。
3. 全てのdefault auto-sync重要分岐を、local保存・current到達性・delivery・warningの観測まで追う。manual syncとauto-syncを別scenarioとする。
4. binary差替え、source/runner/schemaだけの更新、tool追加、OS/capability欠如、timeoutを注入し、scope外・未実行がfullPASSへ到達しない。
5. install receiptと実行前後digestを結合し、異なるcheckoutのSHAだけを付けたbinaryを同一buildと認定しない。
6. fault injectionの起動証拠と実OSの実行を残し、helper未起動やskipを並行性試験のPASSにしない。
7. report再集計は保存済みraw verdictを改変せず、過去の合格を新しい規則へ無条件転用しない。
8. private fixtureを含むoutputから公開summaryを生成したとき、秘密鍵・環境パス・非公開URLが混入しない。

閾値や網羅数は本提案で捏造しない。既存scopeのbaselineと、必要な新obligationの根拠から採用時に固定する。最終CI・配布QAの再実行は別の実装継続トラックであり、本監査を「再実行済み」と扱わない。

## 7. Phase / audit closure

Phase6はPARTIAL。Phase4/5へ戻す判断は「full acceptanceのscopeとplatform profile」「result/statusの外部意味」「provenanceを何に結合するか」。Go AST lintの局所改善は既存ルールを実装する変更としてCONDITIONALLY_READYだが、本提案はそのコードを書いていない。

対象closure roots: static-boundary enforcement、binary/source evidence、MCP inventory→semantic outcome、local保存→auto-sync、platform選択→aggregate、process status→human approval。mutation全実行、全OS harness、remote forge実機、全dependencyの再現ビルドは未実施。現行の限定証拠を否定することと、その証拠の射程を広げないことを区別する。


## 後続管理

import lintの反例は開発補助bugとして別バックログに分離し、現候補runtimeの追加実装には含めない。最小反例・AST置換の期待・リスク評価を独立Issue原稿に保存した。製品runtimeのcanonical codec迂回を確認したという所見にはしない。
