# ADR-0035: forge の pre-receive は system git で隔離領域を読み、受け入れ規則は MIT の app 層に置く

- 状態：採用
- 日付：2026-11-15
- 関連：[ADR-0002](0002-go-and-hybrid-git-access.md)（go-git と system git の分担）、[ADR-0010](0010-licensing.md)、[ADR-0019](0019-forge-in-monorepo.md)、[12-forge-server.md](../12-forge-server.md)「push パイプライン」

## 文脈

[ADR-0019](0019-forge-in-monorepo.md)「帰結」は、P8 着手時に次を別 ADR で決めるとしていた。

- pre-receive で、隔離領域（`GIT_OBJECT_DIRECTORY` と `GIT_ALTERNATE_OBJECT_DIRECTORIES`）にある未受理のオブジェクトをどう読むか。

git は pre-receive フックの実行中、push で届いたオブジェクトを隔離領域に置き、フックの環境変数でその場所を渡す。
go-git はこれらの環境変数を解釈しないため、go-git でリポジトリを開くと新しいコミットが見えない。

P8 の最初の実装は、[12](../12-forge-server.md)「push パイプライン」のうち、検証側の不具合修正を待たずに先行できる 2 つ（ADR-0019「帰結」）に絞る。

1. 名前空間：`refs/githive-remote/**` を常に拒否し、`namespace_mode`（既定 `open`、オプトインで `strict`）で他の名前空間の受け入れを決める。
6. fast-forward：`refs/projects/**` の非 fast-forward 更新を常に拒否する。

あわせて、これらの規則のコードをどの層・どのライセンスに置くかを決める必要がある。

## 決定

### 1. 隔離領域のオブジェクトは system git（`core/gitx`）経由で読む

pre-receive の検査で必要なオブジェクト読み出しは、`core/gitx` の system git 呼び出しで行う。
`gitx` は親プロセスの環境を引き継いで git を起動するので、git がフックに渡した隔離領域の環境変数がそのまま効く。
fast-forward の判定は既存の `gitx.Runner.IsAncestor`（`git merge-base --is-ancestor`）を使う。

go-git に alternates を明示的に渡す案は、今回の検査（祖先判定）では採らない。
署名・スキーマ・ツリー照合（パイプラインの 3〜5）で go-git の読み出しが必要になったら、その時点で `old..new` の範囲を検証する API とあわせて見直す。
[ADR-0002](0002-go-and-hybrid-git-access.md) の線引き（通信は system git、ローカル操作は go-git）は変えない。
隔離領域は git がフックのために用意する一時的なオブジェクト格納であり、go-git で開く通常のローカルリポジトリではないため、この線引きの例外として扱う。

### 2. 受け入れ規則は MIT の `internal/app/receiveapp` に置く

ref 更新列の解析（pre-receive の標準入力の形式）と、名前空間・fast-forward の判定は `internal/app/receiveapp` に置く。
`internal/forge/hook` はフックの入出力（標準入力の読み取り、拒否理由の stderr 出力、終了コード）だけを担う。

理由は 2 つある。

- 層構造：クライアント層の `internal/forge` は `gitx` を直接呼べない（[01](../01-architecture.md)「依存規則」）。
- ライセンス：「`refs/projects/**` は fast-forward のみ」「`refs/githive-remote/**` は push 不可」はデータ形式の約束であり、互換サーバーや GitHub の ruleset 等でも同じ判定を再現できるのが望ましい。[ADR-0010](0010-licensing.md) は仕組みを誰でも使えるようにすることを要求しているので、MIT の側に置く。

### 3. ref の削除はこの段階では拒否しない

[12](../12-forge-server.md) が常に拒否するとしているのは `refs/projects/**` の force push であり、削除は policy の action（push/create/delete/force）で扱う。
policy の評価（パイプラインの 2）は未実装なので、削除は当面受け入れる。
policy 評価の実装時に、`refs/projects/**` の削除の既定を決める。

### 4. namespace_mode はフックの引数で渡す

forge の設定ファイル（TOML）は未実装なので、`githive-forge hook pre-receive --namespace-mode=strict --allow-namespace=refs/notes/` のように引数で渡す。
設定ファイルを導入するときは、同じ値を設定から読むようにし、引数は上書き用として残す。

## 帰結

- pre-receive の検査は、フックの環境変数を消さずに `gitx` へ渡す必要がある。`gitx.Runner` が環境を引き継ぐ現在の実装を変えるときは、隔離領域の読み出しが壊れないことを `internal/forge/hook` の結合テストで確認する。
- 拒否コードは `reserved_namespace`、`namespace_not_allowed`、`non_fast_forward` の 3 つから始める。拒否されたら push 全体を失敗させる（git の pre-receive は ref ごとの部分受理をしない）。
- fold 意味論・イベント形式・リポジトリ schema_version は変更しない。
