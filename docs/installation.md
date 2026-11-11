# インストール・更新・アンインストール

## 対応する配布方法

現在は Go によるソースからのビルドとインストールに対応する。
公開リリースのバイナリ、Homebrew パッケージ、自動更新コマンドはまだ提供していない。
必要な環境は Go 1.25 以上と Git 2.34 以上。SSH 署名を使う場合は OpenSSH も必要になる。

```sh
go version
git --version
go install github.com/ymsaki/githive/cmd/githive@latest
```

GitHub 上の所有者表記が `YmSaki` でも、Go モジュールのパスは **`github.com/ymsaki/githive`** と小文字で指定する。大文字のパスでは `module declares its path as` エラーになる。
公開タグがない期間の `@latest` は開発中のリビジョンを指す。安定版リリースを意味しない。

## PATH と起動確認

インストール先は `go env GOBIN` で確認する。空なら `go env GOPATH` で表示される作業領域の `bin` ディレクトリになる。
そのディレクトリを PATH に加え、新しいターミナルから確認する。

```sh
githive --help
githive issue --help
githive mcp serve --help
```

コマンド名は `githive`。`git hive` を起動するための `git-hive` 実行ファイルは配布していない。
MCP の設定でも `command` は `githive`、`args` は `["mcp", "serve"]` とする。GUI アプリから PATH が見えない場合は `command` にインストールした実行ファイルの絶対パスを指定する。

## 導入したリビジョンの確認

現在の CLI に `--version` はない。Go のビルド情報から導入したモジュールのバージョンを確認できる。

```sh
# macOS / Linux（PATH から実行ファイルを検索）
go version -m "$(command -v githive)"
```

PowerShell では `go version -m (Get-Command githive).Source` を使う。
`mod github.com/ymsaki/githive` の行が導入したバージョンで、タグがない場合はコミットを含む疑似バージョンになる。
ローカル checkout からビルドした場合は `(devel)` と表示されるため、同じ出力の `vcs.revision` と `vcs.modified` も確認する。

## 更新と以前のリビジョンへの戻し方

更新前に上記のビルド情報を控え、MCP サーバーなど実行中の githive を停止してから再インストールする。Windows では実行中のファイルを置き換えられないことがある。

```sh
go install github.com/ymsaki/githive/cmd/githive@latest
githive --help
```

導入するリビジョンを固定する場合は `@latest` を `@<commit>` または控えた疑似バージョンに置き換える。
CLI の置き換えだけではリポジトリ内のデータは移行されない。新しい版で書き込んだデータを古い版で扱えるとは限らないため、旧版に戻す場合は [互換性規則](01-architecture.md#バージョニングと互換性) を確認する。
更新後に MCP クライアントを再起動すると、新しい実行ファイルが使われる。

## データを保持したアンインストール

MCP サーバーを停止し、クライアントの MCP 設定から githive のエントリーを外す。
次に、インストール先を確認して **実行ファイルだけ** を削除する。

```sh
# macOS / Linux：まず表示されたパスが今回のインストール先か確認する
command -v githive
# 確認した絶対パスを指定して実行する
rm /absolute/path/to/githive
```

Windows は確認したインストール先の `githive.exe` を削除する。
専用に加えた PATH エントリーが不要なら外す。他の Go ツールと共有する `bin` ディレクトリは削除しない。

Issue などの記憶は各リポジトリの Git オブジェクトと `refs/projects/*` にあり、実行ファイルの削除では消えない。
`githive init` が加えた fetch refspec と `core.logAllRefUpdates` も残る。再インストールすれば同じリポジトリの記憶を利用できる。
データを残す場合は `.git`、`refs/projects/*`、Git オブジェクトを削除しない。
