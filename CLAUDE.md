# githive 実装ガイド（Claude Code / 実装エージェント向け）

P0〜P2・P5・P3 は実装済み（MVP + 署名まで完了）。P4（wiki/運用コマンド）着手中。
現在のフェーズ状況は docs/13-roadmap.md と GitHub issues（`task`/`from-review` ラベル）を確認すること。

## 最初に読むもの

1. docs/README.md（全体の目次と読む順序）
2. docs/13-roadmap.md（実装フェーズ。P0 から順に進め、フェーズを飛ばさない）
3. docs/16-coding-conventions.md（Go の命名・構成規約）
4. 着手フェーズの対象設計書（P0 なら docs/02、docs/03、docs/14）

## 絶対に守る不変条件

- 決定的実体化：同じイベント集合からは byte 単位で同一の実体化ツリーが得られること（docs/02）。
- fold 意味論を変える変更は schema_version の昇格を必須とする（docs/01）。
- canonical JSON の実装は 1 箇所に集約し、encoding/json を直接使わない（docs/14）。
- 改行は LF 固定。.gitattributes がこれを強制する。

## 実行可能スペック（spec/）

spec/ は言語中立の実行可能スペックである。
`python3 spec/validate.py` が全 PASS することを常に維持する。
Go 実装は spec/vectors/ のゴールデンベクタと出力が一致する golden テストを持つこと（canonical JSON は byte 一致、fold は構造一致）。
仕様の解釈に迷ったら docs/02 → spec/reference/ の順に正とする。

## 作法

- コミットは実装単位で分け、メッセージは日本語（例「core/event: canonical JSON encoder を実装」）。
- 設計と異なる実装が必要になったら、先に docs/adr/ に ADR を追加して理由を残す。
- テストなしのフェーズ完了は無い。各フェーズの exit criteria は docs/13-roadmap.md に定義済み。

## 開発ワークフロー（issue → 実装 → レビュー → マージ）

このリポジトリは issue 起票から PR マージまでを半自動化している。新しく着手する Agent は以下を把握しておくこと。

- **issue 起票**：`.github/ISSUE_TEMPLATE/task.yml`（実装タスク）・`bug-report.yml`（バグ報告）。目的・受け入れ条件・影響範囲・テスト観点を必須項目とする。
- **issue トリアージ**：`.github/workflows/claude-issue-triage.yml` が起票・コメントに反応し、情報充足・スコープ適正・重複・テスト観点をチェックする（`triage:*` ラベルで状態管理）。
- **PR 自動レビュー**：`.github/workflows/auto-pr-review.yml` が PR ごとに CLAUDE.md の不変条件・層構造・テストを確認し、blocking/non-blocking/nit に分類して報告する。non-blocking 指摘は `from-review` ラベルで自動起票される。
- **実装〜マージの自走ループ**：`.claude/skills/work-issue`・`watch-pr`・`merge-pr`（`disable-model-invocation: true`、`/work-issue <issue番号>` 等で人間が明示的に起動する）。**マージの実行は常に人間の承認を要求する設計**——`watch-pr` は判断のみ、`merge-pr` は承認済み前提でしか動かない。
- **機械的強制**：`.claude/rules/`（determinism.md・go-layering.md・spec-sync.md）が該当パスのファイル編集時に自動で文脈注入される。`.claude/hooks/post-edit-go.sh` は `.go` ファイル編集直後に gofmt と canonical JSON 集約規則（`scripts/check-canonical-json.sh`）を検証し、`.claude/hooks/pre-push-gate.sh` は `git push` 実行前に `go test ./...` と `python spec/validate.py` を通す（いずれもローカルの `.git/hooks/pre-push` とは別物。後者は `origin/main` の取り込み漏れを防ぐ個人環境限定のフックで、`.claude/` 管理外）。
- **並列実装の注意**：複数 issue を並行して進める場合、触るファイルが重ならないことを確認してから並列化する（`Agent` に `isolation: "worktree"` を指定）。ファイルが重なる issue は順番にやる。
