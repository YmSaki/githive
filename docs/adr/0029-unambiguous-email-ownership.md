# ADR-0029: email の所有者が曖昧な台帳で認証を許可しない

## 状態

採用（Issue #92 の identity 衝突に関する保存・認証境界の保護）。

## 背景

email は actor と SSH principal を結ぶ identity である。一つの email を複数 username が共有すると、別のユーザーの鍵と admin ロールが合算され、Agent の鍵で管理者の email を名乗る署名が有効と判定される。

## 決定

- 一つの email の所有 username は一つに限る。比較は現在の actor と同じ byte 一致とし、case folding や暗黙の email 正規化は追加しない。
- 同じユーザーの複数 email は正当な alias として維持する。同じユーザーの配列内の重複は所有者の衝突とは数えない。
- 署名検証の allowed_signers はそのコミットの email だけに絞る。一人のユーザーにある別 alias が先に出力されても、その正当な署名を別 principal と誤判定しない（#111）。
- アプリケーションのユーザー・Agent 登録では、他 username が既に所有する email を保存前に拒否する。競合時の再試行ごとに現在の台帳を再確認する。Agent の事前検査でも拒否し、不要な鍵を生成しない。
- 生の履歴・並行する端末の更新から複数所有が生じた場合、ユーザーを選んで自動修復しない。曖昧な email の鍵・admin 判定は fail-closed とし、fsck / verify が `ambiguous_email` と所有者を報告する。正当な他の email の検証は可能なままにする。
- fold、既存のイベント、実体化ツリーは変更しない。schema_version は据え置く。これは不正な identity の受理・認証境界の制約である。

## 制限と回復

既存の曖昧な所有関係は履歴から削除しない。運用者が元のユーザーと鍵を確認し、正当な所有関係になる明示的な user_set で解消する必要がある。衝突中の email を修復用の admin として自動的に信頼しない。

CLI/MCP の email 追加・削除専用操作、task --mine 等の全 alias 判定の統一は、この境界修正とは別の Issue #92 の残課題である。既に存在する一人のユーザーの複数 email を否定するものではない。
