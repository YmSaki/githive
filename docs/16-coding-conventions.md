# 16. コーディング規約

このドキュメントは、既存コードから読み取れる Go の命名・構成パターンを明文化したものである。
アーキテクチャ上の依存方向・層構造は [01-architecture.md](01-architecture.md) と
[.claude/rules/go-layering.md](../.claude/rules/go-layering.md) が正であり、本ドキュメントは重複しない。

発端: `cmd/githive/mcp.go` に `githive log` の MCP ツール登録が、無関係な
`registerNotifyTools` 内に混在して追加される事故があった（PR #31 レビュー指摘、issue #34）。
新しい機能を既存の似た関数に「間借り」させず、以下の規約に従って専用の単位を切ることを徹底する。

## internal/app/* パッケージ（アプリケーション層）

- コンストラクタは常に `func New(dir string) *Service` とする。`Service` 以外の型名（`Client`、`Handler`等）は使わない。
- 一覧取得のフィルタ型は `type ListFilter struct{}` とする。単一コレクションしか持たない feature（issue/comments、task/notes、chat/messages、notify/posts、logapp）はこの1つの型で足りる。
- センチネルエラーは `var ErrXxx = errors.New("<パッケージ名>: <小文字の説明>")` の形式で宣言する。パッケージ名プレフィックスは省略しない（例: `chatapp.ErrNotFound = errors.New("chatapp: chat thread not found")`）。呼び出し側は `errors.Is` で判定する。
  - 既存の逸脱例: `logapp.ErrInvalidSince` はプレフィックスを欠いていた。本規約の明文化に合わせて修正する。
- 他パッケージのセンチネルエラーを再エクスポートする場合（`ErrIdentityNotConfigured = identity.ErrNotConfigured` 等）はそのままの形を踏襲する。

## cmd/githive/*.go（CLIコマンド層）

- サブコマンドのコンストラクタは `func new<Feature><SubCommand>Cmd() *cobra.Command` とする。トップレベルコマンドは `func new<Feature>Cmd() *cobra.Command`（例: `newIssueCmd`、`newIssueNewCmd`、`newIssueListCmd`）。
- 単一サブコマンドしか持たない機能は `func new<Feature>Cmd() *cobra.Command` のみでよい（例: `newLogCmd`、`newStatusCmd`）。

## cmd/githive/mcp.go（MCPツール層）

- ツール登録関数は `func register<Feature>Tools(server *mcp.Server, dir string)` とし、`registerMcpTools` から呼ぶ。
- **1 feature = 1 register関数を基本とする。** 新しいツールを追加するときは、既存の似た関数に追記する前に、その機能が独立した feature かどうかを判断すること。
- 複数の小さな機能をまとめてよい例外は、それぞれが単体では登録関数を持つほどの規模でない場合に限る（例: `registerVerifyAndWhoamiTools`、`registerSyncAndStatusTools` — いずれも1〜2ツールずつの小機能を意図的に束ねたもの）。この例外を機能追加のたびに拡大解釈しない。
- ツールのパラメータ型は `type <feature><SubCommand>Params struct` とし、`register<Feature>Tools` と同じセクション（`// ---- <feature> ----` コメントで区切る）に置く。

## テスト関数名

- `func Test<対象関数またはメソッド><検証内容>(t *testing.T)` とする（例: `TestListFilterSince`、`TestListInvalidSince`、`TestListSkipsCheckpoints`）。
- e2e（CLIバイナリ経由）のテストは `TestCLI<コマンド><検証内容>` とする（例: `TestCLILogJSONShape`）。

## コメントの言語

- パッケージ doc コメント・エクスポートされる型/関数の doc コメントは英語散文で書き、このリポジトリ固有の設計判断を指す箇所だけ日本語の設計文書該当節を「」で引用する（例: `// (docs/10-cli-spec.md「コマンド体系」: ...)`）。コメント全体を日本語にしない。
- 非公開関数内の実装コメント（なぜこう書いたかの説明）は日本語でよい。既存コードでもこの使い分けが一貫している。

## コミットメッセージ

[CLAUDE.md](../CLAUDE.md) 「作法」を参照。実装単位で分け、日本語で書く。
