# 15. 公式クライアント群

CLI（[10](10-cli-spec.md)）に加え、TUI、MCP サーバー、VSCode 拡張を公式クライアントとして提供する。
全クライアントは `internal/app` のサービス層を共有し、機能の意味論を再実装しない。

```
CLI  ------\
TUI  -------+--> internal/app --> internal/core --> git
MCP  ------/         ^
VSCode 拡張 --(子プロセス: githive --json)--+
```

## TUI

- `githive tui` で起動する。CLI と同一バイナリに同梱し、配布物を増やさない。
- 実装は bubbletea + lipgloss を想定する。
- 画面：ダッシュボード（未読 notify、自分の task、最近の issue）、issue 一覧/詳細/コメント投稿、task ボード（todo/doing/review/done の列）、chat スレッド表示/投稿、sync 状態。
- wiki は閲覧のみ（編集はエディタに委ねる）。
- キーバインドは vim 風を既定とし、ヘルプを `?` で常時表示できるようにする。

## MCP サーバー

AI Agent（Claude、Codex 等）向けの高速路として MCP を提供する。

- `githive mcp serve` で stdio トランスポートのサーバーとして起動する。同一バイナリに同梱。
- ツールは CLI のコマンド体系と 1:1 に対応させる（`issue_list`、`issue_comment`、`task_status`、`notify_list_unread`、`chat_post`、`sync` 等）。入出力スキーマは CLI の `--json` の data と同一とする。
  この対応を守ることで、「MCP が使えない Agent は CLI --json を使えば同じことができる」という等価性（[00](00-vision.md) の人間と Agent の対等）を保つ。
- 読み取り系ツールにはページングと絞り込みを持たせ、Agent のコンテキストを浪費させない。
- リソースとして `githive://issue/<id>` 等の URI を公開し、wiki の `githive:` リンク（[features/wiki.md](features/wiki.md)）と対応させる。

### 配布形態

- 単体：`githive mcp serve` を MCP クライアント設定に登録するだけで動く（追加ランタイム不要。Go 単一バイナリの利点）。
- **Claude plugin 形式**：plugin マニフェスト + MCP サーバー定義 + スキル（githive の使い方を教える SKILL.md）を同梱したパッケージを配布する。スキルには「clone 直後に `githive init` と `githive status` を実行して文脈を得る」等の運用知識を書く。また、いくつかの操作のために専用スキルを追加することや、サブエージェントの定義なども行えればと思う。
- **Codex plugin 形式**：同じ MCP サーバーを Codex の設定形式でラップして配布する。
- plugin パッケージは `clients/plugins/` 配下で管理し、リリース CI が githive 本体のバージョンに追従して生成する。plugin 形式の仕様変更に本体が影響されないよう、マニフェスト生成はテンプレートで分離する。

## VSCode 拡張

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
