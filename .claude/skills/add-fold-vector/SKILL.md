---
name: add-fold-vector
description: 新しい kind の fold ゴールデンベクタを spec/ と Go 側の両方に定型手順で追加する。fold 規則を追加・変更したときに手動で使う。
disable-model-invocation: true
allowed-tools: ["Read", "Write", "Edit", "Bash", "Grep", "Glob"]
---

# /add-fold-vector $ARGUMENTS

`$ARGUMENTS` に対象の feature 名（例 `chat`、`notify`、`users`）を渡して使う。[[spec-sync]] の同期義務を満たすための定型手順。既存の issue/task 実装（`spec/reference/fold_issue.py`、`spec/vectors/fold-issue/`）に倣う。

## 手順

### 1. `spec/reference/fold_<kind>.py` を作成する

- `spec/reference/fold_issue.py` を雛形にする。冒頭 docstring に対象 feature の fold 規則の要約（対応する `docs/features/<kind>.md` の節を参照）を書く。
- `events_common.sort_events` で ID（ULID）昇順ソートしてから畳み込む。
- `.checkpoint` で終わる kind は必ず無視する（`internal/core/materialize/materialize.go` の汎用ルールと同じ）。
- 対応する Go reducer（`internal/core/materialize/<kind>.go`）を読み、フィールドの初期値・LWW／集合演算／単純合併のどれに当たるかを 1 つずつ突き合わせる。Go 側と Python 側で挙動が 1 つでもずれていたら、それはどちらかにバグがあるということなので、まず `docs/features/<kind>.md` を正として判定する。

### 2. `spec/vectors/fold-<kind>/` にベクタを追加する

- 命名規則：`01-basic-*.json`、`02-...`、のように連番 + 説明的な名前。
- 最低限、次のケースをカバーする。
  - 基本的な作成 → 更新 → 終端状態への遷移（`01-basic-*.json`）
  - LWW（後勝ち）が効くフィールドの競合編集
  - 集合演算（add/remove がある場合）
  - シャッフル不変性ケース（`spec/vectors/fold-issue/03-shuffled-input-same-result.json` 相当。events 配列を時系列と異なる順で並べておき、`spec/validate.py` 側で 3 回シャッフルして再検証させる）
  - checkpoint が実体化結果に影響しないことを確認するケース
- 各ベクタは `events`、`expected_meta`、および対象 feature のコレクションキー（issue なら `expected_comments`、task なら `expected_notes`。chat なら `expected_messages`、notify なら `expected_posts` のように feature に合わせて命名する）を持つ。

### 3. `spec/validate.py` へ配線する

- `iter_all_events()` の `for sub in ("fold-issue", "fold-task"):` に対象ディレクトリ名を追加する。
- `run_fold_validation()` に `_run_fold_dir(results, VECTORS_DIR / "fold-<kind>", fold_<kind>.fold, "expected_meta", "expected_<コレクション>")` の呼び出しを追加する。
- ファイル冒頭の `import fold_issue` の並びに `import fold_<kind>` を追加する。

### 4. `internal/core/materialize` の Go golden テストに配線する

- `internal/core/materialize/materialize_test.go` の `runFoldVectorDir` ヘルパーが使えるなら（対象 feature が単一コレクションを持つ場合）、`TestFoldIssueVectors` / `TestFoldTaskVectors` に倣って `TestFold<Kind>Vectors` を追加するだけでよい。
- 対象 feature が複数コレクションを持つ場合（例：users の `users`/`groups`）は `runFoldVectorDir` をそのまま使えないため、両方のコレクションを個別に canonical JSON 化して比較する専用テストを書く（[[determinism]] 参照）。

### 5. 両方 green を確認する

```
python spec/validate.py
go test ./internal/core/materialize/...
```

両方が全 PASS になるまで、Python 参照実装・Go reducer・ベクタの 3 者を突き合わせる。ズレが見つかったら、まず `docs/features/<kind>.md` の記述と照らして正しい方を判定してから直す。
