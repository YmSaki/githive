# ADR-0013: チェックポイントイベントの封筒（kind / entity）の規約

- 状態：採用
- 日付：2026-07-13

## 文脈

issue #50 は `githive fsck --compact` を実装するものである。[03-sync-and-concurrency.md](../03-sync-and-concurrency.md)「チェックポイント」は、チェーンが長くなったときに fold の起点を毎回ルートまで遡らずに済むよう、**直前のチェックポイントから 500 イベントまたは 90 日経過**でチェックポイントコミットを追記すると定めている。仕様は次を要求する。

- kind は `<機能>.checkpoint`。
- data に「この時点の完全な状態」と「包含するイベント数とハッシュ」を持つ。
- チェックポイントは fold で常に無視される読み出しの近道であり、実体化結果を変えてはならない（[determinism ルール](../../.claude/rules/determinism.md)「チェックポイントは fold で常に無視する」、`internal/core/materialize/materialize.go` の `.checkpoint` サフィックス一律スキップ）。

一方 `internal/core/event/envelope.go` の `Envelope.Validate` は、**すべての**イベント封筒に対して有効な 26 文字 ULID の `entity`、`^[a-z]+\.[a-z_]+$` に一致する `kind`、非 nil の `data` オブジェクトを要求する。チェックポイントも実体としては 1 個のイベントコミットであり、この検証を通らなければならない。したがって「チェックポイントの entity に何を入れるか」はデータモデル上の決定であり、暗黙に導入せず記録する必要がある（issue #50 の設計監査指摘）。

問題は、機能によってチェーンの entity の持ち方が異なることである。

- **per-entity チェーン**（issue / task / chat, `refs/projects/<機能>/<id>`）：チェーン内の全イベントが ref の ULID を `entity` に共有する。
- **シングルトンストリーム**（notify `refs/projects/notify/stream`, users `refs/projects/users/registry`）：チェーン全体を指す単一の entity id が存在しない。既存の実装では、たとえば `notifyapp` の `notify.post` / `notify.ack` は各イベント自身の ULID を `entity` に入れている（`Entity: eid`）。

## 決定

チェックポイント封筒の規約を次のとおり固定する。

- **kind**：`<機能>.checkpoint`（例 `issue.checkpoint`, `chat.checkpoint`, `notify.checkpoint`, `users.checkpoint`）。`^[a-z]+\.[a-z_]+$` に一致し、`materialize` の `.checkpoint` 一律スキップに自動的に乗る。個々の reducer には一切手を入れない。
- **id / ts**：`idgen.NewWithTimestamp()` が返す新規の単調増加 ULID とそれに対応する ts。他のイベントと同じ採番規則。
- **entity**：
  - per-entity チェーン（issue / task / chat）では、そのチェーンの entity id（ref の ULID）を再利用する。チェックポイントは「そのエンティティの」読み出しの近道である、という意味に合致する。
  - シングルトンストリーム（notify / users）では、チェックポイント**自身のイベント id** を entity に入れる。これはストリームに単一の entity id が無いための規約であり、かつ既存の notify の「entity == 自イベント id」の慣習と一致する。
- **data**：`{"event_count": <包含する非チェックポイントイベント数>, "head": <直前のヘッドコミット OID>, "state": {"meta": …, "collections": …}}`。仕様の「完全な状態＋イベント数＋ハッシュ」を満たす。ただし data はあくまで読み出しのヒントであり、正となる実体化状態はチェックポイントコミットのツリー（各機能の `TreeFiles` が描画する正規レイアウト）である。

チェックポイントコミットのツリーは、汎用の `core/merge.TreeFiles` ではなく**各機能自身の `TreeFiles`**（front matter・jsonl 等の正規レイアウト）で描画する。fold がチェックポイントを無視するため `TreeFiles(fold(events + checkpoint)) == TreeFiles(fold(events))` が成り立ち、チェックポイント追記後のヘッドツリーは追記前とバイト単位で同一になる。汎用レイアウトを使うと、チェックポイントを持つクローンと持たないクローンで同一イベント集合の実体化ツリーが分岐し、[02-data-model.md](../02-data-model.md)「決定性の不変条件」に違反する。

## 帰結

- チェックポイント追記は、CAS 再試行付き追記の共通実装 `internal/app/entitychain.Writer` にそのまま乗る（[go-layering ルール](../../.claude/rules/go-layering.md)「CAS 再試行付き追記は internal/app/entitychain を使う」）。`fsckapp` は per-機能の `Registry` と `TreeFiles` を Writer に渡すだけでよい。
- entity の規約が per-entity とシングルトンで分岐することを `fsckapp` の `featureWriter.perEntity` が表現する。将来チェックポイントの entity を fold が参照するようになる設計変更（現状は無関係）を入れる場合は、本 ADR を昇格・改訂する。
- チェックポイント透過性（有無で fold 結果が変わらないこと）は `internal/app/fsckapp` のプロパティテストと、追記前後の実体化状態一致テストで機械的に守る（determinism ルール「checkpoint 透過性」に準拠）。
- schema_version（`Envelope.SchemaVersion`）の昇格は不要である。チェックポイントは新しい fold 意味論を導入せず、既存の一律スキップ規則に乗るだけで、同一イベント集合の実体化結果を変えないため（determinism ルール「fold 意味論を変える変更は schema_version の昇格が必須」の対象外）。
