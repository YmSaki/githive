# 監査基点後の追加ファイルと検証の区別

実装基点 `dfc4ebe6ecf48c700d83bd54c9556db4bdf2b0e9`。分担ledgerは各観測epochの記録であり、新規ファイルを暗黙に既読へ加算しない。

| 追加ファイル | 確認した責務・観測範囲 |
|---|---|
| `cmd/githive/display_ids.go` | 一意な表示ID、全collectionでの比較、読込後ref消滅時は完全IDへ戻す。表示層のみ、参照解決規則を増やさない。担当実装/回帰＋統合読取り。 |
| `cmd/githive/auto_sync_outcome_test.go` | 置換失敗・kept-local警告・MCP明示同期・部分配送と保存状態の実CLI検査。1db77fcのUbuntu/macOS全suiteで実行成功。 |
| `cmd/githive/file_body_test.go` | LF/複数LF/CRLFのIssue本文・コメント・Task noteを実保存して完全履歴から比較。鍵入力の正規化は別経路。1db77fcの全suiteで成功。 |
| `cmd/githive/wiki_local_conflict_test.go` | 新競合のexit3/nonretryableと両OID/worktree metadataの分類。静的確認、最終CIで実行確認する。 |
| `internal/app/syncapp/registry_recovery_test.go` | reflog無し/GC後保持、backup再利用・異先/lock拒否、CAS競合、非送信。1db77fcの全suiteで成功。 |
| `internal/app/wikiapp/advance.go` | current読取→祖先検査→検査済みexactcurrentのCAS。両保存末尾から呼び、失敗時push/cleanupしない。root/独立レビューで精読。 |
| `internal/app/wikiapp/advance_test.go` | 別localwriterをpublic Saveとpush末尾へ注入、拒否時の両履歴・worktree保持、明示merge後の再開。静的確認、最終CIで実行確認する。 |

`internal/core/projectconfig/version.go` / `version_test.go` / `internal/core/gitx/revision_errors_test.go` は [spec側追補](spec-and-build.md) と独立レビューで別に確認した。version parserは十進文字列で整数を判定し、巨大指数を無制限展開せず、未来版の元表記を失わない。Go実行失敗を不存在へ写像しない。

`scripts/distribution-acceptance.py` とCIの独立jobは最後に追加した検証補助。head SHAと公開moduleの実版を照合し、旧版からの更新・実行ファイルのみ削除・再導入後のrefs/config/Issueとbinary digest、全41tool、署名付きMCP、設定例のliteralを検査する。公開artifactはsummary/provenanceと検査したbinaryだけで、raw fixtures/秘密鍵は含めない。独立staticレビューと1db77fcの実CIで確認した。一般の全flags/全攻撃を網羅したとの主張ではない。

## 対象ファイルの照合

下の数はGit追跡されたGo source inventoryと分担報告・この追加表のパスを照合したもの。意味検証の深さと実行証拠は各報告に従う。ファイル名の一致自体を安全性の証明にはしない。

| 範囲 | production | tests |
|---|---:|---:|
| cmd | 20 | 45 |
| internal/app | 52 | 77 |
| internal/cliout | 1 | 1 |
| internal/core | 37 | 45 |
| spec | 1 | 0 |
