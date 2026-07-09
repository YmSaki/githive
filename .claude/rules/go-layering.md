---
paths: ["**/*.go"]
---

# Go の層構造規則

githive は 4 層構成である（[docs/01-architecture.md](../../docs/01-architecture.md)「全体構成」）。

```
クライアント層  cmd/githive, internal/tui, internal/mcpserv, clients/vscode
アプリケーション層  internal/app/*（issueapp, taskapp, notifyapp, chatapp, usersapp, wikiapp, syncapp）
コア層  internal/core/*（event, chain, materialize, merge, refspace, sign, registry, gitx, identity, idgen）
git 層  go-git（ローカル）+ system git（通信、internal/core/gitx 経由）
```

## 依存方向は一方通行

- 依存は `cmd` → `app` → `core` の一方向のみ。逆方向の import は禁止。
- `internal/core` パッケージから `internal/app` を import してはならない。`core/materialize` と `core/merge` は純粋関数として書き、I/O（git 読み書き）は `core/chain` に隔離する（依存規則）。
- クライアント層（`cmd/githive`、TUI、MCP サーバー）は `app` のサービスだけを呼び、`core` を直接呼ばない。例外は VSCode 拡張で、プロセス境界（CLI `--json`）越しに `app` を利用する。
- 通信（fetch/push/clone）は `core/gitx` 経由で system git を呼ぶ。go-git はローカルのオブジェクト・ref 操作にのみ使う（[ADR-0002](../../docs/adr/0002-go-and-hybrid-git-access.md)）。

新しいパッケージを追加するときは、まずこの表のどの層に属するかを決め、依存が下方向だけになっているか確認する。`go vet` や CI の import 検査には現状この方向性チェックは含まれないため、レビュー観点として自分で確認すること。

## 共通化は「2 例目まで複製、3 例目で抽出」

feature 間（issue/task/chat/notify/users）で似た処理が必要になっても、1〜2 個目の feature では複製してよい。共通パターンとして抽出するのは 3 個目の feature が同じ形を必要としたときにする（[docs/13-roadmap.md](../../docs/13-roadmap.md)「P2：task / chat / notify」の作業分解、「早すぎる抽象化」の手当て）。

早すぎる抽出は、まだ揺れている要件を無理に固定してしまい、後で feature ごとの差異が出たときに抽象化を壊す方が高くつく。判断に迷ったら複製を選ぶ。

実例：`internal/app/entitychain`（`internal/app/entitychain/entitychain.go`）は、issueapp・taskapp・chatapp・notifyapp が共通で必要とした「fold → tree 化 → commit → CAS で ref 前進、競合時は再試行」ループを、3 機能目が同じ形を必要とした時点で抽出したもの（パッケージ doc コメント参照）。

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

`Writer.Append` はプロセス内の同時書き込みを直列化する in-process ロック（Windows で go-git のローカルオブジェクト書き込みが並行に弱いための対策）と、プロセス間競合に対する git 側 CAS（`gitx.ErrRefCASMismatch` での再試行、最大 10 回）の両方を扱う。同じ形のループを feature ごとに再実装しないこと（[docs/03-sync-and-concurrency.md](../../docs/03-sync-and-concurrency.md)「クラッシュ安全性とローカル競合」、[docs/14-testing.md](../../docs/14-testing.md) シナリオ11）。

singleton ref（例：notify/stream）を扱う場合は `RefFor` が id を無視して常に同じ ref 名を返すようにする。
