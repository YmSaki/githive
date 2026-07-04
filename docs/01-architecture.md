# 01. アーキテクチャ

## 全体構成

githive は 4 つの層で構成する。
下の層ほど安定させ、上の層は下の層だけに依存する。

```
+----------------------------------------------------------+
| クライアント層                                              |
|  cmd/githive (CLI) | TUI | MCP server | VSCode 拡張        |
+----------------------------------------------------------+
| アプリケーション層  internal/app                            |
|  issue / task / notify / chat / users / wiki の各サービス   |
|  sync オーケストレーション                                   |
+----------------------------------------------------------+
| コア層  internal/core                                      |
|  event | chain | materialize | merge | refspace |          |
|  sign | registry | gitx                                    |
+----------------------------------------------------------+
| git 層                                                     |
|  go-git（ローカルオブジェクト操作） + system git（通信）       |
+----------------------------------------------------------+
```

サーバー（forge）は別バイナリとして同じコア層を再利用する（[12](12-forge-server.md)）。
クライアント層のうち CLI 以外（TUI、MCP、VSCode 拡張）の詳細は [15-clients.md](15-clients.md) に置く。

## リポジトリ構成（モノレポ）

```
githive/
  cmd/
    githive/            # CLI エントリポイント（TUI と MCP はサブコマンドとして同梱）
    githive-forge/      # forge サーバー（後続フェーズ）
  internal/
    core/
      refspace/         # ref 名の構築・解決・列挙
      event/            # イベント封筒の定義、canonical JSON codec、スキーマ検証
      chain/            # コミットチェーンの読み書き（go-git）
      materialize/      # kind ごとの fold 関数（イベント集合 -> 状態ツリー）
      merge/            # イベント和集合マージの実装
      sign/             # SSH 署名の付与と検証
      registry/         # users/groups/policy の読み出しと評価
      gitx/             # system git の呼び出し（fetch/push/credential）
    app/
      issueapp/ taskapp/ notifyapp/ chatapp/ usersapp/ wikiapp/
      syncapp/          # fetch -> merge -> push の再試行ループ
    cliout/             # human/JSON 出力の整形
    tui/                # TUI 実装（bubbletea 想定）
    mcpserv/            # MCP サーバー実装（stdio）
  clients/
    vscode/             # VSCode 拡張（TypeScript、CLI --json を子プロセスとして利用）
    plugins/            # Claude plugin / Codex plugin のマニフェストとテンプレート
  docs/
```

## 依存規則

- `core` は `app` と クライアント層を import しない。
- `core/materialize` と `core/merge` は純粋関数として書く。I/O（git 読み書き）は `chain` に隔離する。
- 通信（fetch/push/clone）は `gitx` 経由で system git を呼ぶ。go-git はローカルのオブジェクト・ref 操作にのみ使う（理由は [ADR-0002](adr/0002-go-and-hybrid-git-access.md)）。
- クライアント層は `app` のサービスだけを呼ぶ。`core` を直接呼ばない。
  VSCode 拡張のみ例外的にプロセス境界（CLI `--json`）越しに `app` を利用する。

## データフロー

### 読み取り（高速路と互換路）

専用ツールは 2 つの読み方を使い分ける。

1. **スナップショット読み**：ref 先頭コミットのツリーから `meta.json` 等を直接読む。一覧表示はこれで足りる。O(エンティティ数)。
2. **イベント読み**：コミットメッセージの JSON を先頭から遡って読む。履歴表示と検証に使う。チェックポイント（[03](03-sync-and-concurrency.md)）以降だけ読めばよい。

人間は素の git で同じデータを読む。

```
git log refs/projects/issue/01j8x...            # イベント履歴
git show refs/projects/issue/01j8x...:meta.json # 現在状態
git show refs/projects/issue/01j8x...:body.md   # 本文
```

### 書き込み

1. `app` サービスがイベント（JSON）を構築する。
2. `materialize` が「現ツリー + 新イベント」から新しい状態ツリーを計算する。
3. `chain` が新コミット（message = イベント、tree = 状態）を作り、ref をローカルで進める。SSH 署名は設定に応じて `sign` が付与する。
4. 同期は書き込みと分離する。`syncapp` が fetch、必要ならマージ、push を行う（[03](03-sync-and-concurrency.md)）。

書き込みはローカル ref の更新までで完了とし、ネットワーク失敗が書き込みを失敗させない。

## 状態を持つ場所

githive はワークツリーを使わない。
全データは bare 相当の参照（refs とオブジェクト）だけで完結し、ユーザーの作業ツリーを汚さない。
例外は wiki で、編集時のみ一時 worktree を作る（[features/wiki.md](features/wiki.md)）。

ローカル設定は git config（`githive.*` キー）に置く。
プロジェクト共有設定は `refs/projects/meta/config` の `config.json` に置く（[02](02-data-model.md)）。

## エラー処理方針

- `core` はエラーを型付きで返す（`ErrNonFastForward`、`ErrSchemaViolation`、`ErrSignatureInvalid` 等）。panic はプログラミングエラーに限る。
- `app` はエラーを利用者操作の単位に翻訳し、再試行可能かどうかを付与する。
- CLI はエラー種別を終了コードに対応させる（[10](10-cli-spec.md)）。
- 部分的成功（例：push は失敗したがローカル書き込みは成功）は正常系として報告する。

## バージョニングと互換性

- イベント封筒に `"v": 1` を持たせる。フィールド追加は同一バージョン内で許し、意味変更・削除はバージョンを上げる。
- 読み手は未知フィールドを保存したまま無視する（forward compatible）。未知の `v` は読み取り拒否し、ツールの更新を促す。
- `refs/projects/meta/config` の `schema_version` はリポジトリ全体の最低要求バージョンを表す。
- **fold 意味論の変更（同じイベント集合から異なる実体化ツリーが生まれる変更）は、schema_version の昇格を必須とする**。この規則が無いと、新旧ツールの混在チームで実体化ツリーが分岐し、verify が偽の改竄検出をする。fold の互換性は golden テスト（[14](14-testing.md)）で機械的に守る。
- Go モジュールはツール本体のバージョンで、データスキーマはこの `v` と `schema_version` で、それぞれ独立に管理する。

## プラットフォーム

Linux、macOS、Windows を等しくサポートする。
開発者環境が Windows であるため、CI は 3 OS で回す。
パス処理は `path`（ツリー内は常に `/`）と `filepath`（ローカル FS）を厳密に使い分ける。
system git は 2.34 以上（SSH 署名対応）を要求する。
