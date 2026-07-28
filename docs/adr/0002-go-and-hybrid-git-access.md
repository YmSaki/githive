# ADR-0002: Go を採用し、git アクセスはハイブリッドにする

- 状態：採用
- 日付：2026-07-04

## 文脈

実装言語と、git オブジェクト・通信の扱い方を決める必要がある。
将来、同じコードベースから forge サーバー（[12](../12-forge-server.md)）を作る。

## 決定

1. 言語は Go とする。
2. ローカルのオブジェクト・ref 操作は go-git で行う。
3. ネットワーク操作（fetch / push / clone）と署名検証は system git を子プロセスとして呼ぶ。

## 理由

- Go は単一バイナリ配布（CLI / TUI / MCP を 1 バイナリに同梱）と、Gitea 等で実証済みの git サーバー実装経路を持つ。実装を担う AI Agent にとっても学習データが豊富で生成品質が安定する。
- go-git はローカル操作には十分だが、認証（ssh-agent、credential helper、2FA トークン）の互換性が system git に及ばない。通信を system git に委ねれば、利用者の既存 git 設定がそのまま使える。
- 署名検証（allowed_signers）も git の実装に委譲し、暗号処理を自前で持たない（[11](../11-security.md)）。

## 帰結

- 実行環境に git 2.34 以上を要求する。起動時に検査し、無ければ終了コード 5 で案内する。
- go-git と system git の挙動差（ref lock、pack 形式）は結合テストで実 git を使うことで検出する。
- 子プロセス呼び出しのオーバーヘッドは通信時のみで、読み取り性能には影響しない。

## 退けた代替案

- **Rust**：性能と堅牢性は最高だが、forge 実装の前例が少なく、エコシステム（gitoxide のサーバー側）が未成熟。
- **TypeScript/Node**：ランタイム配布が必要で、git サーバー実装に不向き。VSCode 拡張のみ TypeScript を使う。
- **全部 system git（go-git なし）**：plumbing 呼び出しの子プロセス数が読み取り性能を悪化させる。
- **全部 go-git**：認証互換の問題が利用者体験を直撃する。

## 追記：`internal/core/gitx` が実際に担う範囲

決定 3 は「ネットワーク操作と署名検証を system git に委ねる」と書いたが、実装が進むにつれ `internal/core/gitx.Runner` は次も system git 経由で担うようになった（`internal/core/gitx/gitx.go:409-416` の自己申告コメント参照）。

- **wiki 用 worktree 操作**（`WorktreeAdd`/`WorktreeRemove`、worktree 上の add/commit/diff/merge、`MergeWikiRemote`）。wiki は event sourcing を使わない唯一の機能で、通常の git ブランチと 3-way マージで運用するため、worktree のチェックアウトやマージはファイルシステム/plumbing 操作そのものであり、go-git よりも system git の挙動に素直に従う。
- **ローカルオブジェクトの生成**（`emptyRootCommit`、`CommitAll`、`StageAll`）。go-git ではなく system git の commit 機構をそのまま使う。
- **読み出し**（`Show`、`Log`、`RevParse`、`IsAncestor`、`ForEachRef`）。ネットワークを伴わない読み取りだが、go-git の実装コストより system git の呼び出しの方が単純だった箇所に system git を使っている。

したがって決定 3 は「ネットワーク操作と署名検証」に限定されず、実際には「go-git は決定的な実体化（materialize/merge が読み書きするコミット・ツリー生成）に、system git はそれ以外（通信、wiki の worktree・plumbing、各種読み出し）に使う」という運用に近い。この使い分けは [ADR-0002] の理由（認証互換性、実装コスト）とも整合するため、決定自体の変更ではなく実態の追記として記録する。
