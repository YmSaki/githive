# githive 実装ガイド（Claude Code 向け）

全エージェント共通の設計・実装原則および文書間の権威順位は [AGENTS.md](AGENTS.md) を参照する。本書は Claude Code 固有の実装手順・スキル・フック運用を定める。

P0〜P2・P5・P3 は実装済み（MVP + 署名まで完了）。P4（wiki/運用コマンド）着手中。
現在のフェーズ状況は [docs/13-roadmap.md](docs/13-roadmap.md) と GitHub issues（`task`/`bug`/`from-review` ラベル）を確認すること。

## 最初に読むもの

1. [AGENTS.md](AGENTS.md)（最上位の開発規範・基本原則・権威順位）
2. [docs/README.md](docs/README.md)（設計文書の一覧と読む順序）
3. [docs/13-roadmap.md](docs/13-roadmap.md)（実装フェーズ。P0 から順に進め、フェーズを飛ばさない）
4. [docs/16-coding-conventions.md](docs/16-coding-conventions.md)（Go の命名・構成規約）
5. 着手フェーズ・タスクの対象設計書（`docs/01`〜`04`、`docs/features/*.md`）

## 実行可能スペック（spec/）

`spec/` は言語中立の実行可能スペックである。
`python3 spec/validate.py` が全 PASS することを常に維持する。
Go 実装は `spec/vectors/` のゴールデンベクタと出力が一致する golden テストを持つ（canonical JSON は byte 一致、fold は構造一致）。
仕様と実装の解釈順位は [AGENTS.md](AGENTS.md)「文書と仕様の権威順位」に従う。

## 作法

- コミットは実装単位で分け、メッセージは日本語（例「core/event: canonical JSON encoder を実装」）。
- 設計と異なる実装が必要になったら、実装前に `.claude/skills/adr` の手順で `docs/adr/` に「検討中」の ADR を作成し、承認後に実装へ進む。
- テストなしのフェーズ完了は無い。各フェーズの exit criteria は [docs/13-roadmap.md](docs/13-roadmap.md) に定義済み。

## 開発ワークフロー（issue → 実装 → レビュー → マージ）

このリポジトリは issue 起票から PR マージまでを半自動化している。新しく着手する Agent は以下を把握しておくこと。

- **issue 起票**：`.github/ISSUE_TEMPLATE/task.yml`（実装タスク）・`bug-report.yml`（バグ報告）。目的・受け入れ条件・影響範囲・設計契約・テスト観点を必須項目とする。
- **issue トリアージ**：`.github/workflows/claude-issue-triage.yml` が起票・コメントに反応し、情報充足・スコープ適正・重複・テスト観点をチェックする（`triage:*` ラベルで状態管理）。
- **PR 自動レビュー**：`.github/workflows/auto-pr-review.yml` が PR ごとに AGENTS.md / CLAUDE.md の不変条件・層構造・責務分担・テストを確認し、blocking/non-blocking/nit に分類して報告する。non-blocking 指摘は `from-review` ラベルで自動起票される。
- **実装〜マージの自走ループ**：`.claude/skills/work-issue`・`watch-pr`・`merge-pr`（`disable-model-invocation: true`、`/work-issue <issue番号>` 等で人間が明示的に起動する）。**マージの実行は常に人間の承認を要求する設計**——`watch-pr` は判断のみ、`merge-pr` は承認済み前提でしか動かない。
- **機械的強制**：
  - `.claude/rules/`（`determinism.md`・`go-layering.md`・`spec-sync.md`）が該当パスのファイル編集時に自動で文脈注入される。
  - `internal/layering_test.go` が `go test ./...` 実行時に層間の依存方向とクライアント層の `core` 直接呼び出し制約を検証する。
  - `.claude/hooks/post-edit-go.sh` は `.go` ファイル編集直後に `gofmt` と canonical JSON 集約規則（`scripts/check-canonical-json.sh`）を検証する。
  - `.claude/hooks/pre-push-gate.sh` は `git push` 実行前に `go test ./...` と `python spec/validate.py` を通す（ローカルの `.git/hooks/pre-push` とは別物）。
- **並列実装の注意**：複数 issue を並行して進める場合、触るファイルパスの重複がないことだけでなく、**同じドメイン規則・設計契約・共有パッケージの契約に触れていないか**も確認してから並列化する（`Agent` に `isolation: "worktree"` を指定）。競合する issue は直列に進める。
