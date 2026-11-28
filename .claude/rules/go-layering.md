---
paths: ["**/*.go"]
---

# Go の層構造規則

githive は 4 層構成である（[docs/01-architecture.md](../../docs/01-architecture.md)「全体構成」）。

```
クライアント層  cmd/githive, cmd/githive-forge, internal/forge, internal/tui, internal/mcpserv, clients/vscode
アプリケーション層  internal/app/*（issueapp, taskapp, notifyapp, chatapp, usersapp, wikiapp, syncapp）
コア層  internal/core/*（event, chain, materialize, merge, refspace, sign, wikifs, gitx, identity, idgen）
git 層  go-git（ローカル）+ system git（通信、internal/core/gitx 経由）
```

## 依存方向は一方通行

- 依存は `cmd` → `app` → `core` の一方向のみ。逆方向の import は禁止。
- `internal/core` パッケージから `internal/app` を import してはならない。`core/materialize` と `core/merge` は純粋関数として書き、I/O（git 読み書き）は `core/chain` に隔離する（依存規則）。
- `internal/core` と `internal/app` から `internal/forge` を import してはならない。MIT のクライアントも Forge 専用コード・`web/` を依存に含めない。共用する処理は MIT の `core` または `app` に置く（[ADR-0019](../../docs/adr/0019-forge-in-monorepo.md)）。
- クライアント層（`cmd/githive`、`cmd/githive-forge`、TUI、MCP サーバー）は `app` のサービスだけを呼び、`core` を直接呼ばない。例外は VSCode 拡張で、プロセス境界（CLI `--json`）越しに `app` を利用する。
- 上記の例外として、**状態を持たない薄い変換・解決に限り** クライアント層から `core` を直接呼んでよい。許容リストは `identity.Resolve`／`event.Encode`・`DecodeGeneric`・`SchemaVersion`（canonical JSON の一元化のため）／`refspace` の定数と `Parse`・`RemoteTrackingRef`・`LocalRefFromTracking`／`core` 各パッケージのセンチネルエラー値（終了コード対応付けのため）。git I/O（`gitx`）・`chain`・`materialize`・`merge` は必ず `app` 経由にする（[docs/01-architecture.md](../../docs/01-architecture.md)「依存規則」に同じ表がある）。
- 通信（fetch/push/clone）は `core/gitx` 経由で system git を呼ぶ。go-git はローカルのオブジェクト・ref 操作にのみ使う（[ADR-0002](../../docs/adr/0002-go-and-hybrid-git-access.md)）。

新しいパッケージを追加するときは、まずこの表のどの層に属するかを決め、依存が下方向だけになっているか確認する。層間のパッケージ import 方向・純粋性、クライアント層の許可外 `core` パッケージ import、および `cmd/githive/root.go` の `chain`/`gitx`/`sign` センチネルエラー参照は `internal/layering_test.go`（`go test ./...`）で機械検証される（許可済みパッケージ内の個別関数単位の制限はレビューで確認する）。

## 共通化の判断基準

- **同じドメイン規則、不変条件、利用者操作は最初から 1 箇所で管理する**。CLI と MCP の 2 箇所しかなくても、同じドメインロジックや操作調停を複製してはならない。
- 「2 例目まで複製、3 例目で抽出」の原則は、**責務が異なり、将来の共通性がまだ確定していない処理**にのみ適用する（[docs/13-roadmap.md](../../docs/13-roadmap.md)「P2：task / chat / notify」の作業分解、「早すぎる抽象化」の手当て）。

実例：`internal/app/entitychain`（`internal/app/entitychain/entitychain.go`）は、issueapp・taskapp・chatapp・notifyapp が共通で必要とした「fold → tree 化 → commit → CAS で ref 前進、競合時は再試行」ループを抽出したもの（パッケージ doc コメント参照）。逆に、CLI と MCP で通知生成が重複して不整合が起きた事例（`cmd/githive/notify_hooks.go`）のように、同一ドメイン規則は出現回数に関わらずアプリケーション層へ集約する。

## CAS 再試行付き追記は internal/app/entitychain を使う

エンティティのイベント追記（現在のイベント列 + 新イベントを fold → ツリー化 → commit → ref を CAS で前進）を書く feature サービスは、自前でこのループを書かず `internal/app/entitychain.Writer` を使う。

```go
w := &entitychain.Writer{
    Dir:         dir,
    RefFor:      refFor,      // id -> ref 名
    Registry:    someRegistry, // materialize.Registry
    TreeFiles:   treeFiles,    // *materialize.State -> ツリー
    NotFoundErr: ErrNotFound,  // 対象なしを表すエラー（無ければ nil）
}
state, err := w.Append(ctx, id, buildEvent)
```

`Writer.Append` は、sync のマージと共有するプロセス内ロック `chain.LockRepository`（Windows で go-git のローカルオブジェクト書き込みが並行に弱いための対策）と、プロセス間競合に対する git 側 CAS（`gitx.ErrRefCASMismatch` での再試行、最大 10 回）の両方を扱う。同じ形のループを feature ごとに再実装しないこと（[docs/03-sync-and-concurrency.md](../../docs/03-sync-and-concurrency.md)「クラッシュ安全性とローカル競合」、[docs/14-testing.md](../../docs/14-testing.md) シナリオ11）。

singleton ref（例：notify/stream）を扱う場合は `RefFor` が id を無視して常に同じ ref 名を返すようにする。
