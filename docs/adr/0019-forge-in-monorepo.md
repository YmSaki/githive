# ADR-0019: forge は同じリポジトリ・同じ Go モジュールで作り、バイナリは分ける

- 状態：採用
- 日付：2026-10-31
- 改訂：2026-11-12（PR #168 レビュー対応）
- 置換：[ADR-0010](0010-licensing.md) の別リポジトリ配置・貢献先の分離のみ。MIT/AGPL のライセンス選択は維持する。
- 関連：[ADR-0002](0002-go-and-hybrid-git-access.md)（go-git と system git の分担）、[12-forge-server.md](../12-forge-server.md)、[13-roadmap.md](../13-roadmap.md)「Horizon 構成」

## 文脈

[13-roadmap.md](../13-roadmap.md) は forge（P8〜P11）を「実装は別リポジトリ化の候補」としていた。
一方で同じ文書は、P8 が P3 に依存する理由を「pre-receive の権限・署名検証は users 台帳と検証ロジックの再利用だから。別実装にすると仕様の二重管理になる」とし、手戻りリスクの表でも「検証は core に置き、pre-receive はそれを呼ぶだけにする」を P8 の構造要件にしている。

Go の `internal/` の可視性はリポジトリや go.mod の個数ではなく、`internal` の親の import path を共有するかで決まる。同じ親を持つモジュールパスなら別モジュールから利用できるため、別モジュールでは常に利用不能という理由で選択肢を退けない。
ここでは、共有検証ロジックの変更・依存版・CLI と Forge の検証を同じ変更単位で扱うため、同じリポジトリ・同じ Go モジュールを選ぶ。公開パッケージ API としての切り出しは必須にしない。

## 決定

### 1. 同じリポジトリ・同じ Go モジュール

go.mod は 1 つのまま、forge を次の場所に置く。go.work による分割はしない。

```
cmd/githive-forge/     # forge のエントリポイント
internal/forge/        # forge 固有のパッケージ（receive, githttp, sshshell, api, jobs, …）
web/                   # Web UI（React）のソースと go:embed 用パッケージ
```

`internal/forge` はクライアント層（`cmd/githive`・MCP と同列）に属する。
`app` のサービスを呼び、`core` の直接呼び出しは [01-architecture.md](../01-architecture.md)「依存規則」の許容リストに従う。
`core` と `app` は `internal/forge` を import しない。

forge とクライアントの両方が使うロジック（policy.json の評価、範囲を指定した検証など）は `internal/forge` に置かず、`core` または `app` に置く。

### 1a. ライセンス境界と旧決定の置換

[ADR-0010](0010-licensing.md) の別リポジトリ配置と貢献先分離だけを置き換える。core/app・CLI・MCP・クライアント・spec/docs の MIT と、Forge 専用部分の AGPL-3.0-or-later は維持する。

| 対象 | 方針 |
|---|---|
| 共有 core/app、CLI、クライアント、spec/docs | MIT。既存ソース・ルート LICENSE は変更しない |
| 将来の `cmd/githive-forge/`、`internal/forge/`、`web/` の専用ソース | AGPL-3.0-or-later。初回実装時に適用範囲の LICENSE/README・参照ライセンス全文・SPDX 表示を用意する |
| `githive` 配布物 | MIT のクライアント。Forge 専用コード・UI をリンクしない |
| `githive-forge` と同梱 UI の結合配布物 | AGPL の条件に従い、MIT 共有コードや第三者依存の表示・対応ソース提供等の義務も満たす |

同一 go.mod やバイナリ分割だけでライセンス境界を保証したとは扱わない。共有部分の変更は MIT 側へ置き、core/app と MIT のクライアントから Forge 専用コード・UI への依存を禁止する。第三者コード・素材の元ライセンス表示を保持する。MIT スキーマからの生成物も、入力の権利表示と配布条件を確認し、共通定義を AGPL 側から MIT のクライアントへ逆輸入しない。

P8 の最初の実装 PR は、ファイルの適用表示、CLI の直接・推移依存に Forge 専用部分や AGPL のみで提供される依存がないこと、Forge 配布物の notice と必要な対応ソースの範囲を検査する CI を含む。Forge ソースや実配布物はこの ADR では追加しない。

### 2. バイナリは `githive` と `githive-forge` の 2 本

- `githive`：現状のまま。forge の依存（SQLite ドライバ、HTTP サーバー周り、埋め込み UI）をリンクしない。
- `githive-forge`：`serve`、`hook pre-receive` / `hook post-receive`、`shell`（SSH の `command=` から起動）、`admin` をサブコマンドに持つ 1 本のバイナリ。[12](../12-forge-server.md) の図の `githive-shell` も別バイナリにせず、このサブコマンドとする。CI runner（P11）も最初は `runner` サブコマンドとし、リモート runner が必要になった時点で分離を再検討する。

SQLite は cgo 不要のドライバを使い、`go build` だけでクロスコンパイルできる現状を保つ。

### 3. Web UI は静的 SPA を go:embed で同梱する

[12](../12-forge-server.md)「Web UI」の方針（React、静的ビルドを go:embed）を具体化する。

- Vite + React + TypeScript の SPA とする。SSR を伴うフレームワークは Node の実行時依存を持ち込むので使わない。
- ソースとロックファイルは `web/` に置く。リポジトリ直下に package.json は置かない。
- `go:embed` は親ディレクトリを参照できないので、`web/` 直下に embed 宣言だけを持つ小さな Go パッケージを置く。
- `web/dist/` にはスタブの index.html だけをコミットする。Node が無い環境（Windows の CI、pre-push ゲート、CLI だけ触る開発者）でも `go build ./...` と `go test ./...` が通るようにするためである。実際のビルド成果物はリリース時とフロントエンドの CI でだけ作る。
- イベント形式の TypeScript 型は `spec/schemas/*.schema.json` から生成し、手書きしない。

### 4. CI

- Go のジョブ（vet、test、canonical JSON lint）は変更パスで絞らず、常にリポジトリ全体で回す。core を CLI と forge が共有しているため、絞ると「core の変更が forge を壊した」を見逃す。
- フロントエンドのジョブ（型チェック、lint、テスト、ビルド、スキーマから生成した型の差分なし確認）は `web/**` と `spec/schemas/**` の変更時だけ回す。
- sshd などを要する forge の結合テストは Linux 限定とし、Windows では build のみ確認する。

## 理由

- 検証ロジックを二重に持たないことが P8 の構造要件であり、同じモジュールならそれを import の形で強制できる。
- core の変更が forge を壊したとき、同じ PR の `go test ./...`、pre-push ゲート、PR 自動レビューで検出できる。別リポジトリでは、forge 側で依存を上げるまで気づけない。
- バイナリを分けておけば、CLI 利用者が forge の依存を背負わない。Go はリンクされないパッケージをバイナリに含めないので、同じモジュールでもこの分離は保てる。

## 帰結

- go.mod/go.sum と依存版解決は CLI/Forge で共有する。Forge 専用パッケージは CLI にリンクされないが、共有依存の版選択や toolchain の変更は CLI にも影響し得るため、全体の Go 検査を省略しない。
- 層構造の表（[01-architecture.md](../01-architecture.md)、`.claude/rules/go-layering.md`）に `internal/forge` を加える。バイナリが 2 本になると逆方向の import が入り込みやすくなるので、「`core`・`app` から `internal/forge` を import しない」の機械的な検査を forge の最初の実装 PR で CI に加える。
- 次の 2 点は本 ADR では決めず、P8 着手時に別の ADR で決める。
  - pre-receive で、隔離領域（`GIT_OBJECT_DIRECTORY` と `GIT_ALTERNATE_OBJECT_DIRECTORIES`）にある未受理のオブジェクトをどう読むか。go-git はこれらの環境変数を解釈しないため、system git 経由で読むか go-git に alternates を明示的に渡すかを、ADR-0002 の線引きとあわせて決める。あわせて、ref ではなく `old..new` の範囲を検証する API を `core`/`app` に設ける。
  - forge の HTTP API・LFS batch API・WebHook ペイロードで使う通常の JSON の扱い。`scripts/check-canonical-json.sh` は現状 `internal/core/event` 以外での `encoding/json` を禁止している。イベントのエンコードは引き続き `event.Encode` に限ったうえで、forge の HTTP 境界に限って許可リストを設ける方向で検討する。WebHook に入れるイベント封筒は再エンコードせず、チェーンから読んだ canonical バイト列をそのまま埋め込む。
- pre-receive の署名・スキーマ・ツリー照合（[12](../12-forge-server.md)「push パイプライン」の 3〜5）は CLI の `verify` と同じ検証を使うので、監査で見つかった検証側の不具合（#78、#109、#110、#113、#115、#156 など）が直るまで着手しない。名前空間（1）と fast-forward（6）の強制、受け口（SSH・smart HTTP）、Web UI の骨組みは先行できる。

## 退けた代替案

- **別リポジトリ**（[13](../13-roadmap.md) の旧記述）：同じ検証ロジックを再利用する構成も可能だが、依存版更新と複数リポジトリの CI を同期する運用が必要になる。現段階では同じ変更単位で検証する構成を優先する。
- **同じリポジトリ内の別 Go モジュール（go.work）**：import path によっては `internal/` を利用できるが、モジュールごとの依存版・ビルド・テストとリリースの管理が増える。現段階では単一モジュールを選ぶ。バイナリ分割はリンク依存を分ける手段であり、モジュール依存やライセンス義務を自動的に分離するものではない。
- **CLI と forge を 1 本のバイナリにする**：CLI 利用者が SQLite や埋め込み UI などサーバー側の依存とサイズを背負う。配布物を 1 つにする利点は、forge 側のサブコマンド集約（決定 2）で十分得られる。
- **SSR を伴うフロントエンド（Next.js 等）**：forge に Node の実行時依存を持ち込み、[12](../12-forge-server.md)「デプロイ形態」の単一バイナリ配布と矛盾する。
