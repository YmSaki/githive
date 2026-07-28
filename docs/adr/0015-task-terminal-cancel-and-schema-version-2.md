# ADR-0015: 終端状態からの cancelled を禁止し、封筒 schema_version を 2 に上げる

- 状態：採用
- 日付：2026-07-28

## 文脈

[features/task.md](../features/task.md)「ステータス機械」は同一節の中で矛盾していた。遷移図は「任意 -> cancelled」と書き、その直後の散文は「終端状態から `cancelled` への遷移イベントは不正遷移として無視する」と書いていた。

実装（`internal/core/materialize/task.go` と `spec/reference/fold_task.py`）は揃って図の側を採っており、`done -> cancelled` も `cancelled -> cancelled` も通る。発注者に確認したところ、意図は散文の側、すなわち**終端からの cancelled は禁止**であった。

これは同じイベント集合から生成される実体化ツリーが変わる変更である。[determinism ルール](../../.claude/rules/determinism.md)「fold 意味論を変える変更は schema_version の昇格が必須」に該当し、定数の昇格・新旧差分ベクタ・本 ADR が必要になる。

昇格には障害がひとつある。`internal/core/event/envelope.go` の `Validate` は `e.V != SchemaVersion` という**完全一致**で封筒を拒否する。定数を素朴に 2 にすると、既存の spec ベクタ（すべて `v: 1`）も既存リポジトリの全イベントも読めなくなる。`spec/schemas/envelope.schema.json` の `"const": 1` も同じ問題を持つ。

## 決定

### 1. 終端状態からの `task.status`(cancelled) は不正遷移として無視する

`done` / `cancelled` を from とする `cancelled` への遷移を、既存の不正遷移とまったく同じ扱いにする。つまり黙って無視し、`status` も `updated_at` も `status_history` も動かさない。エラーにはしない（fold の基本則）。

非終端（`todo` / `doing` / `review` / `blocked`）からの `cancelled` は従来どおり許可する。`done` / `cancelled` からの `todo`（再開）も従来どおり許可する。これは [features/task.md](../features/task.md)「再開は `todo` への遷移イベントで表す」の明示的な要求であり、本 ADR の対象外である。

### 2. `event.SchemaVersion` を 1 から 2 に上げ、受理規則を範囲判定に変える

`Validate` の `v` 判定を `e.V != SchemaVersion` から `1 <= e.V <= SchemaVersion` に変える。

- **既知の古い v は受理する**。v:1 で書かれた既存イベントは引き続き読め、fold できる。
- **未知の（より新しい）v は拒否する**。[01-architecture.md](../01-architecture.md)「バージョニングと互換性」の「未知の `v` は読み取り拒否し、ツールの更新を促す」と [determinism ルール](../../.claude/rules/determinism.md) の同旨に従う。
- **書き込みは常に現行 `SchemaVersion`** で行う。各 app が `V: 1` とハードコードしていた箇所を `V: event.SchemaVersion` に置き換える。

`spec/schemas/envelope.schema.json` の `v` は `"const": 1` から `"enum": [1, 2]` にする。

### 3. fold は `v` でディスパッチしない

イベントの `v` に関わらず、常に現行の遷移表を適用する。バージョンごとの fold 実装を分岐させて持つことはしない。

この帰結として、**旧ツールで作られた既存データに `done -> cancelled` 遷移が含まれる場合、新旧ツールで実体化結果が分岐し `githive verify` が偽の改竄を検出する**。githive はプレリリースであり運用中のデータが存在しない前提なので、この非互換を受容する。移行スクリプトは用意しない。

### 4. 新ツールが書く v:2 は旧ツールに拒否される

旧ツールの `Validate` は完全一致なので v:2 を読めない。これは意図した非互換検出であり、上記 3 の分岐を黙って起こすよりよい。

### 5. 封筒の残作業を同じ変更にまとめる

`v` の意味論に触るついでに、封筒まわりで見つかっていた 2 点も片付ける。

- `envelope.go` 冒頭のコメントが「docs/02: 未知の v は読み取り拒否」を根拠として引用していたが、その記述は [02-data-model.md](../02-data-model.md) には無い。正しい根拠は [01-architecture.md](../01-architecture.md)「バージョニングと互換性」と determinism ルールである。コメントの参照先を直し、あわせて docs/02 の封筒の節にも受理規則（既知の古い v は受理、未知の新しい v は拒否）を明記する。
- `data` の欠落を Go の `Decode` が空オブジェクト補完で受理していたが、`envelope.schema.json` は `data` を required としている。**spec が正**の原則に従い、Decode でも欠落を拒否する。githive 自身は常に `data` を書くため実データへの影響は無い。

## 理由

- 遷移表の自己矛盾は、docs・spec・Go の三者が同じ表を共有するこのリポジトリでは放置できない。どちらかに倒す必要があり、発注者の意図は「終端は終端」であった。
- 完全一致から範囲判定への変更は、schema_version を上げるたびに全データが読めなくなる設計を、今後の昇格でも使える形に直すものである。1 回目の昇格である今がその変更に最も安いタイミングである。
- fold を `v` で分岐させないのは、分岐させた瞬間に「どのバージョンで書かれたか」が実体化結果に影響し、[02-data-model.md](../02-data-model.md)「決定性の不変条件」の前提（同じイベント集合 → 同じツリー）が「同じイベント集合かつ同じ v 分布 → 同じツリー」に弱まるためである。

## 帰結

- `meta/config` の `schema_version`（`initapp.SchemaVersion`）は**昇格しない**。[01-architecture.md](../01-architecture.md)「バージョニングと互換性」のとおり封筒の `v` とは独立に管理する値であり、リポジトリ全体の最低要求バージョンを表す。今回の変更はリポジトリの形式要求を変えていない。
- `spec/vectors/fold-task/05-terminal-cancelled-ignored.json` が新旧の差分を固定する。旧意味論では FAIL し新意味論でのみ PASS するので、後方非互換が意図されたものであることが機械的に記録される。このベクタのイベントは `v: 2` で書き、範囲受理の回帰テストも兼ねる。
- 既存のベクタ（すべて v:1）と既存テストは無変更で通り続ける。これが「既知の古い v は受理」の実地確認になる。
