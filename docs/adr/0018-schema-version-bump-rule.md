# ADR-0018: fold 意味論の変更では「封筒 v」と「リポジトリ schema_version」の両方を上げる

- 状態：採用
- 日付：2026-10-31
- 関連：[ADR-0013](0013-checkpoint-envelope-convention.md)、[ADR-0015](0015-task-terminal-cancel-and-schema-version-2.md)、[ADR-0017](0017-wiki-and-meta-sync.md)

## 文脈

「fold 意味論を変える変更は schema_version の昇格を必須とする」という不変条件（CLAUDE.md、[01-architecture.md](../01-architecture.md)「バージョニングと互換性」）で、「schema_version」が 2 つの別の値を指して使われていた（issue #156）。

- **封筒の `v`**（`internal/core/event.SchemaVersion`）。イベントごとに書かれる。
- **`refs/projects/meta/config` の `config.json` の `schema_version`**（`internal/app/initapp.SchemaVersion`）。リポジトリ全体に 1 つ。

[ADR-0015](0015-task-terminal-cancel-and-schema-version-2.md) は封筒の `v` だけを上げ、config 側は「昇格しない」とした。一方 [ADR-0017](0017-wiki-and-meta-sync.md) は、新しい意味論のイベントを古い意味論で読む状態を防ぐのは config 側だと論じており、両者は矛盾していた。

封筒の `v` だけでは分岐を防げない。旧ツールが止まるのは、新しい `v` のイベントを含むチェーンを読んだときだけである。旧 `v` のイベントだけでできたチェーン（例：旧ツールが書いた `done -> cancelled`）は、新旧のツールで実体化結果が黙って分岐する。しかも config は古いままなので、旧ツールは書き込みも続けられる。

既存の feature に新しい kind を足す場合の扱いも決まっていなかった。旧ツールは未知の kind を無視し（`materialize.Registry.Fold`）、新ツールは描画するので、これもツリーの分岐を生む。

さらに、config の検査は書き込み（`entitychain.Writer.Append`）でしか行っていなかった。読み取り（`show`）と sync のマージも fold を行うので、同じ分岐が起きうる。

## 決定

### 1. 用語を分ける

- **封筒 v**：イベント封筒の `v` フィールド。定数は `event.SchemaVersion`（書く値）と `event.MinSchemaVersion`（読める最古の値）。
- **リポジトリ schema_version**：`refs/projects/meta/config` の `config.json` の `schema_version`。リポジトリ全体の最低要求バージョンを表す。定数は `initapp.SchemaVersion`（このビルドが対応する最大値）。

文書で単に「schema_version」と書かず、どちらかを明示する。どちらも定数の現在値を文書に直書きしない（コードと spec スキーマが正）。

### 2. fold 意味論を変える変更では両方を上げる

「fold 意味論を変える変更」は、同じイベント集合から生成される実体化ツリーが変わる変更である。次を含む。

- ステータス遷移表、初期値、畳み込み順序などの変更。
- **既存の feature の reducer に、ツリーに影響する新しい kind を追加すること**。旧ツールはその kind を無視するので、ツリーが分岐する。

次は含まない。

- fold が常に無視する kind の追加（`.checkpoint` で終わる kind、[ADR-0013](0013-checkpoint-envelope-convention.md)）。
- 新しい feature（新しい ref 名前空間）の追加。旧ツールはその ref を読まないので、既存 ref のツリーは分岐しない。
- 既存イベント集合の実体化結果が変わらない、フィールドの追加。

該当する変更では、同じコミット群で次をすべて行う。

1. `event.SchemaVersion` を上げる。`spec/schemas/envelope.schema.json` の `v` の `enum` に新しい値を加える。
2. `initapp.SchemaVersion` を上げる。`spec/schemas/meta.schema.json` の `schema_version` の制約を確認する。
3. 既存リポジトリの `config.json` を新しい値へ引き上げる経路を用意する（下記 4）。
4. 変更前後の fold 結果の差を示す golden ベクタを追加する。
5. ADR を追加する。

### 3. fold を行う操作はすべて、先にリポジトリ schema_version を検査する

このビルドの `initapp.SchemaVersion` より大きい値を見たら、操作を拒否する（終了コード 5、`error.code` は `schema_unsupported`）。

- 書き込み：`entitychain.Writer.Append`（従来どおり）。
- 読み取り：`entitychain.Writer.Fold`（`show` 系と notify/users の一覧）。
- sync：fetch の直後、マージに入る前に、ローカルと追跡側（`refs/githive-remote/meta/config`）の両方を検査する。追跡側が新しければ、何もマージしない。

meta/config が無いリポジトリ（init 前、部分クローン）では検査を省く（従来どおり）。

### 4. 既存リポジトリの引き上げ

リポジトリ schema_version を上げる最初の変更で、次を実装する。

- 新しいツールは、自分の `initapp.SchemaVersion` より小さい `config.json` を見たら、最初の書き込みの前に meta/config へイベントを 1 つ追記して `schema_version` を引き上げる。
- 引き上げは sync で他の clone に伝わる。meta/config のマージは大きい方を採る（[ADR-0017](0017-wiki-and-meta-sync.md)）ので、下がる方向には合流しない。
- 引き上げを受け取った旧ツールは、上記 3 により読み書きと sync を止め、ツールの更新を促す。

現時点では `initapp.SchemaVersion` は 1 で、それより小さいリポジトリは存在しないので、この経路はまだ実装しない。

### 5. ADR-0015 の変更は遡って扱いを変えない

[ADR-0015](0015-task-terminal-cancel-and-schema-version-2.md) は封筒 v だけを 2 に上げ、リポジトリ schema_version は 1 のままとした。本 ADR の規則に照らせば両方を上げるべき変更だったが、ADR-0015 §3 のとおり、プレリリースで運用中のデータが無い前提で分岐を受容済みである。そのため、リポジトリ schema_version は 1 のままとする。次に fold 意味論を変える変更から本 ADR の規則を適用する。

## 理由

- 封筒 v は「このイベントを読めるか」を表す。旧ツールが新しいイベントを読み違えることは防げるが、旧イベントの解釈の違いは防げない。後者を防げるのは、リポジトリ全体に 1 つしかない値だけである。
- 封筒 v も上げ続けるのは、新しいツールが書いたイベントを旧ツールが確実に拒否できるようにするためである。リポジトリ schema_version の引き上げが届く前に、旧ツールが新しいイベントを読む経路（push 済みのチェーンを直接 fetch する場合など）が残る。
- 読み取りと sync も検査するのは、どちらも fold を行うからである。読み取りが誤った状態を見せ、それを根拠に人や Agent が書き込むと、書き込み側の検査だけでは防げない。sync はマージで旧い意味論のツリーを書くうえ、meta のマージで新しい `config.json` をローカルへ持ち込むので、マージ前に止める必要がある。

## 帰結

- CLAUDE.md、[01-architecture.md](../01-architecture.md)、[02-data-model.md](../02-data-model.md)、[13-roadmap.md](../13-roadmap.md)、determinism ルール、githive-concepts スキル、PR 自動レビューの観点は、本 ADR の用語と手順に揃える。
- [ADR-0013](0013-checkpoint-envelope-convention.md) の「`Envelope.SchemaVersion`」という存在しない識別子は、「封筒 v（`event.SchemaVersion`）」と読み替える。チェックポイントは fold が常に無視するので、どちらの昇格も不要という結論は変わらない。
- [ADR-0015](0015-task-terminal-cancel-and-schema-version-2.md)「帰結」の「`meta/config` の `schema_version` は昇格しない」は、上記 5 の経過措置として扱う。今後の昇格の先例にはしない。
- 旧ツールの利用者は、引き上げられたリポジトリでは読み取りもできなくなる。分岐した状態を黙って見せるよりはよい。

## 退けた代替案

- **封筒 v だけを上げる（ADR-0015 の方式）**：旧イベントの解釈の分岐を防げない。
- **リポジトリ schema_version だけを上げる**：引き上げが届いていない旧ツールが、新しいツールが書いたイベントを旧い意味論で読める。封筒 v を範囲判定にした ADR-0015 の仕組みも活かせない。
- **fold を `v` でディスパッチする**：ADR-0015 §3 のとおり、実体化結果が「同じイベント集合」でなく「同じイベント集合かつ同じ v 分布」に依存してしまう。
