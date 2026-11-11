# 許可された private GitHub 環境での Hosted transport QA

対象はソース `c5da0be02e87fa2e221662bea184e77787b1fa12`。後続の修正・最終配布バイナリの合格証拠とは分ける。リポジトリ URL・名称・認証情報はこの要約へ掲載しない。

バイナリ SHA256: `46fa108871cb827290c0af71f7e047604fcb40a0f94841c8058ec3b309476d42`。既存ソース checkout を変更せず、指定 commit の Git archive から別ディレクトリへ build し、Go ビルド情報と archive の hash を記録した。

66コマンド・19チェックが成功し、失敗0件。

- 初期状態で remote に広告された ref がないことを確認。合成データだけを使用し、削除・force push は行っていない。
- GitHub が `meta/config`、`users/registry`、Issue、Task、Wiki の5本の `refs/projects/*` を受け付けた。
- 別の通常 clone は projects refs を暗黙には取得せず、`githive init` の初回 fetch で共有 config を採用し、Issue・Task・Wiki を読めた。
- 両 clone が同じ Issue の共通祖先から offline で別々のコメントを作成。A の push 後、B の sync が divergent history を merge/push し、A の fetch 後に両コメントと全 projects refs が一致した。
- Issue の実体化 tree hash は両 clone で `20d2f34ade944e55169f642e5202eec448b567d1` と一致。
- それぞれ別の SSH 鍵と合成 identity を使い、両 clone で `githive verify`、`githive fsck`、`git fsck --strict` が成功。verify の報告対象は Issue・Task・users 台帳の3本であり、Meta/Wiki を含めた全 ref の署名検証と同義ではない。
- 再 sync は両 clone ともすべて `up-to-date`。remote/local の ref hash は変化しなかった。

これは明示的 sync の transport・収束・署名を検証したもので、暗黙 auto-sync の警告表示、全 flags、すべての権限攻撃やホスティング事業者の動作を網羅しない。Chat/Notify の hosted 配送は最終配布バイナリの追加 probe へ分離する。

実ログ・合成秘密鍵は private なローカル QA 領域に保持する。実 URL を含む raw logs は public PR/docs へ掲載しない。
