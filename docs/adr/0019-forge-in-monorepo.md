# ADR-0019: forge は同じリポジトリ・同じ Go モジュールで作り、バイナリは分ける

- 状態：採用
- 日付：2026-10-31
- 関連：[ADR-0002](0002-go-and-hybrid-git-access.md)（go-git と system git の分担）、[12-forge-server.md](../12-forge-server.md)、[13-roadmap.md](../13-roadmap.md)「Horizon 構成」

## 文脈

[13-roadmap.md](../13-roadmap.md) は forge（P8〜P11）を「実装は別リポジトリ化の候補」としていた。
一方で同じ文書は、P8 が P3 に依存する理由を「pre-receive の権限・署名検証は users 台帳と検証ロジックの再利用だから。別実装にすると仕様の二重管理になる」とし、手戻りリスクの表でも「検証は core に置き、pre-receive はそれを呼ぶだけにする」を P8 の構造要件にしている。

別リポジトリ（または同じリポジトリ内の別 Go モジュール）にすると、`internal/` は越境 import できない。
forge から core を使うには、core を `pkg/` などの公開 API として切り出す必要がある。
core は監査（#67）対応で署名検証・スキーマ検査・チェーン読み取りがまだ動いている最中であり、ここで公開 API として固定するのは早すぎる。

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

- go.sum には forge の依存も載る。CLI のバイナリには影響しない。
- 層構造の表（[01-architecture.md](../01-architecture.md)、`.claude/rules/go-layering.md`）に `internal/forge` を加える。バイナリが 2 本になると逆方向の import が入り込みやすくなるので、「`core`・`app` から `internal/forge` を import しない」の機械的な検査を forge の最初の実装 PR で CI に加える。
- 次の 2 点は本 ADR では決めず、P8 着手時に別の ADR で決める。
  - pre-receive で、隔離領域（`GIT_OBJECT_DIRECTORY` と `GIT_ALTERNATE_OBJECT_DIRECTORIES`）にある未受理のオブジェクトをどう読むか。go-git はこれらの環境変数を解釈しないため、system git 経由で読むか go-git に alternates を明示的に渡すかを、ADR-0002 の線引きとあわせて決める。あわせて、ref ではなく `old..new` の範囲を検証する API を `core`/`app` に設ける。
  - forge の HTTP API・LFS batch API・WebHook ペイロードで使う通常の JSON の扱い。`scripts/check-canonical-json.sh` は現状 `internal/core/event` 以外での `encoding/json` を禁止している。イベントのエンコードは引き続き `event.Encode` に限ったうえで、forge の HTTP 境界に限って許可リストを設ける方向で検討する。WebHook に入れるイベント封筒は再エンコードせず、チェーンから読んだ canonical バイト列をそのまま埋め込む。
- pre-receive の署名・スキーマ・ツリー照合（[12](../12-forge-server.md)「push パイプライン」の 3〜5）は CLI の `verify` と同じ検証を使うので、監査で見つかった検証側の不具合（#78、#109、#110、#113、#115、#156 など）が直るまで着手しない。名前空間（1）と fast-forward（6）の強制、受け口（SSH・smart HTTP）、Web UI の骨組みは先行できる。

## 退けた代替案

- **別リポジトリ**（[13](../13-roadmap.md) の旧記述）：core を公開 API として切り出す必要があり、検証ロジックの変更が forge に届くまでに依存更新の手間と時差が生じる。「検証は core に置き、pre-receive はそれを呼ぶだけ」という P8 の構造要件を import で保証できなくなる。
- **同じリポジトリ内の別 Go モジュール（go.work）**：`internal/` を越境 import できない点は別リポジトリと同じで、core の公開化が必要になる。依存の分離という利点は、バイナリを分ければ単一モジュールでも得られる。
- **CLI と forge を 1 本のバイナリにする**：CLI 利用者が SQLite や埋め込み UI などサーバー側の依存とサイズを背負う。配布物を 1 つにする利点は、forge 側のサブコマンド集約（決定 2）で十分得られる。
- **SSR を伴うフロントエンド（Next.js 等）**：forge に Node の実行時依存を持ち込み、[12](../12-forge-server.md)「デプロイ形態」の単一バイナリ配布と矛盾する。
