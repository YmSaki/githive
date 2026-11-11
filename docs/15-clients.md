# 15. 公式クライアント群

現行のクライアントは CLI（[10](10-cli-spec.md)）と MCP サーバー。TUI と VSCode 拡張は将来の計画である。
全クライアントは `internal/app` のサービス層を共有し、機能の意味論を再実装しない。

```
CLI  ------\
TUI  -------+--> internal/app --> internal/core --> git
MCP  ------/         ^
VSCode 拡張 --(子プロセス: githive --json)--+
```

## TUI（未実装・計画）

- `githive tui` で起動する。CLI と同一バイナリに同梱し、配布物を増やさない。
- 実装は bubbletea + lipgloss を想定する。
- 画面：ダッシュボード（未読 notify、自分の task、最近の issue）、issue 一覧/詳細/コメント投稿、task ボード（todo/doing/review/done の列）、chat スレッド表示/投稿、sync 状態。
- wiki は閲覧のみ（編集はエディタに委ねる）。
- キーバインドは vim 風を既定とし、ヘルプを `?` で常時表示できるようにする。

## MCP サーバー

AI Agent（Claude、Codex 等）向けの高速路として MCP を提供する。

- `githive mcp serve` で stdio トランスポートのサーバーとして起動する。同一バイナリに同梱。
- ツールは CLI のコマンド体系と対応させる（`issue_list`、`issue_comment`、`task_status`、`notify_list` の `unread` 引数、`chat_post`、`sync` 等）。入出力スキーマは CLI の `--json` の data と同一とする。
  この対応を守ることで、「MCP が使えない Agent は CLI --json を使えば同じことができる」という等価性（[00](00-vision.md) の人間と Agent の対等）を保つ。
- `sync` の構造化出力は `items`・`total` と、警告がある場合の `warnings` を持ち、各 item の `warning` は CLI と同じ値を返す。一部失敗時もこの情報を保持して MCP の `isError: true` を返し、`error` に CLI と同じ `code`・`message`・`retryable` と追加診断の `data` を付ける。Go エラーとして返すと MCP SDK が構造化出力を破棄するため、この経路は明示的なエラー結果を返す。
- 読み取り系ツールにはページングと絞り込みを持たせ、Agent のコンテキストを浪費させない。
- 読み取り専用リソースとして `githive://issue/<id>`・`githive://task/<id>`・`githive://chat/<id>` と `githive://wiki/<path>` を公開する。wiki は入れ子のパスを受け付け、空白や `%` などは URI エスケープする（例 `githive://wiki/design/nested%20page.md`）。内容は `wiki_show` と同じ JSON で、非 UTF-8 は `content_base64` を使う。読み出しは worktree 作成・sync・ref 更新を行わない。
  wiki 本文中の `githive:issue/<id>` 形式はクライアント用リンクであり、MCP の `githive://` URI とは別の記法である。本文リンクのジャンプ解決は未実装で、このリソース公開には含まない（[features/wiki.md](features/wiki.md)）。
- 例外：`githive init` は MCP ツールとして公開しない。bootstrap（fetch refspec の追加、meta/config の初期化）は人間が明示的に行う一度きりの操作であり、Agent が自動で叩いてよい操作ではないため（`doctor`・`fsck` は読み取り／保守で安全なため公開する）。

### 配布形態

- 単体：`githive mcp serve` を MCP クライアント設定に登録するだけで動く（追加ランタイム不要。Go 単一バイナリの利点）。
- **Claude plugin のソース**：`clients/plugins/claude/` にマニフェスト・MCP 定義・使い方のスキルを置く。本体バイナリは同梱しない。bootstrap は利用者が明示し、Agent は clone 後に状態・identity・必要な同期を確認する（同梱 README とスキルを参照）。
- **Codex plugin 形式（未提供）**：同じ MCP サーバーを Codex の設定形式でラップする計画。現時点では直接 `githive mcp serve` を登録する。
- plugin の公開リリース生成 CI と、本体バージョンへ自動追従するマニフェスト生成は未実装である。

## VSCode 拡張（未実装・計画）

- 実装は TypeScript。`clients/vscode/` に置く。
- githive 本体とはプロセス分離し、`githive --json` を子プロセスとして呼ぶ。Go 側の API 安定性は JSON 封筒（[10](10-cli-spec.md)）が担う。
  MCP ではなく CLI を使うのは、拡張が必要とする操作粒度が CLI と同じで、MCP のセッション管理を挟む必要がないためである。
- 機能：
  - サイドバー：issue 一覧、自分の task、未読 notify のツリービュー。
  - issue / chat の閲覧と投稿（WebView）。
  - エディタ連携：`githive:issue/<id>` リンクのジャンプ、コミットメッセージへの issue ID 挿入補完。
  - ステータスバー：未 push の ref 数と未読数。
- 拡張は githive バイナリを同梱しない。PATH に無ければインストール手順を案内する。

## 提供順序

クライアントの実装順は CLI → MCP → TUI → VSCode 拡張とする（[13](13-roadmap.md)）。
MCP を TUI より先にするのは、Agent が clone 直後から使えることがこのシステムの中核価値であり、TUI の意義は人間の快適さの改善であるため、必要機能は代替手段（CLI）が存在しているからである。
