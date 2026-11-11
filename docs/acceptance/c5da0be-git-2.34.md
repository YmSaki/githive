# Git 2.34.0 実機 smoke

acceptance の対象バイナリのソースは `c5da0be02e87fa2e221662bea184e77787b1fa12`、SHA256 は `46fa108871cb827290c0af71f7e047604fcb40a0f94841c8058ec3b309476d42`。後続・最終配布版の合格証拠とは区別する。

source-install smoke は別手順であり、その `source.commit` も **`c5da0be02e87fa2e221662bea184e77787b1fa12` に固定**した。移動中の repository HEAD を作業ディレクトリには使わず、明示 commit の `git archive` から展開した source を `--source` に指定した。archive SHA256 は `242245f3c8a4b0dc94f91bb03622cb5cfd87a6a7ab8a5ffe45510562c379484a`。展開後の通常ファイル430件を archive と再照合し、内容の差は0件だった。

source-install が作った実行ファイルの SHA256 も上記 acceptance 対象と一致した。ただし、ソース導入検査と既存バイナリ acceptance の実行記録は分離している。archive からの build は `-buildvcs=false` のため自己申告の `vcs.revision` を持たず、この source pin は明示 archive とコマンド記録によるものである。

Git 公式の `v2.34.0` tag archive を取得し、既存の gcc/make/zlib/curl/OpenSSL を使って作業領域内へ build/install した。依存パッケージの追加や global の Git 置き換えは行っていない。

- archive SHA256: `e4a8012d7f949e8f6405f437c04e37623f35a8019f4038dca2030285be974cce`
- 実行した Git binary SHA256: `534ed587d80308786ddb12aadd56155a824a74545f6148309b358a8338571031`
- `git --version`: `git version 2.34.0`
- 隔離 PATH の source-install smoke: 7コマンド成功、失敗0件。
- 同 PATH の可搬 acceptance: 99コマンド・56 RPC request、登録41ツールの全41種類を呼び出し、4種の resource を読んだ。失敗0件。
- 署名付き CLI/MCP verify、ローカル bare remote と clone、Wiki worktree、Agent setup、通知などの通常 smoke を含む。doctor の実出力でも `git 2.34.0 (>= 2.34.0)` を確認した。

これは Linux の現在のライブラリ上で自前 build した Git 2.34.0 の通常利用証拠であり、全 flags・攻撃・並行性・旧 OS・全 Git バージョンの網羅ではない。Hosted GitHub の transport 試験とは別のローカル fixture 試験である。
