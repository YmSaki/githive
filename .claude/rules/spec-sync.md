---
paths: ["internal/core/materialize/**", "spec/**"]
---

# spec/ と internal/core/materialize の同期義務

`spec/` は言語中立の実行可能スペックであり、Go の fold 実装が従うべき形式的な基準を提供する（[spec/README.md](../../spec/README.md)、CLAUDE.md）。`python3 spec/validate.py` は常に全 PASS を維持しなければならない（CLAUDE.md の絶対条件）。

## fold 規則を追加・変更したら同一コミット群で更新するもの

`internal/core/materialize/*.go` の reducer（fold 規則）を追加・変更するとき、次を **同じコミット群** に含める。1 つでも欠けると `spec/` と Go 実装が乖離し、「python3 spec/validate.py が全 PASS」は見かけ上維持されても、実際には Go 側の新しい振る舞いを何も検証していない状態になる。

1. `spec/reference/fold_<kind>.py` — 既存の `spec/reference/fold_issue.py` / `fold_task.py` に倣った Python リファレンス実装。`events_common.sort_events` で ID 順ソートしてから畳み込み、checkpoint kind は常に無視する。
2. `spec/vectors/fold-<kind>/*.json` — ゴールデンベクタ。命名規則は `01-basic-*.json` のように連番 + 説明的な名前。シャッフル不変性を確認するケース（`03-shuffled-input-same-result.json` 相当）を必ず含める。
3. `spec/validate.py` への配線 — `iter_all_events()` の対象ディレクトリ一覧、`run_fold_validation()` の `_run_fold_dir` 呼び出しに新 kind を追加する。
4. `internal/core/materialize` の Go golden テスト — `materialize_test.go` の `runFoldVectorDir` ヘルパー（単一コレクションの feature ならそのまま呼べる）または個別テストで `spec/vectors/fold-<kind>/` を読み込み、Go の Registry.Fold 結果と突き合わせる。

新 kind 追加の定型手順は `/add-fold-vector` スキルに手順化してある。

## 既知の負債：chat / notify / users の spec ベクタが欠落している

現状、`internal/core/materialize/chat.go`・`notify.go`・`users.go` には reducer と Go テスト（`chat_test.go`、`notify_test.go`、`users_test.go`）が存在するが、対応する `spec/reference/fold_chat.py`・`fold_notify.py`・`fold_users.py`、`spec/vectors/fold-chat/`・`fold-notify/`・`fold-users/`、および `spec/validate.py` への配線が **存在しない**。

`spec/validate.py` は現在 `fold-issue` と `fold-task` しか走査しないため（`spec/validate.py` の `iter_all_events()` / `run_fold_validation()`）、chat/notify/users の fold 規則は言語中立スペックとして未検証のまま Go 実装だけが正になっている。CLAUDE.md の不変条件（決定的実体化）は Go テストでは守られているが、「spec/ が言語中立の実行可能スペックである」という前提を満たしていない。この 3 kind に手を入れる、または手が空いたタイミングで `/add-fold-vector` を使って埋めること。

なお users は `users`/`groups` の 2 コレクションを持つため、`runFoldVectorDir` を素朴に流用できない（[[determinism]] 参照）。両コレクションを比較する専用の golden テストが必要になる。

参照：[docs/02-data-model.md](../../docs/02-data-model.md)、[spec/README.md](../../spec/README.md)。
