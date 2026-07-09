---
name: release-check
description: v0.1 リリース前チェック（バージョン文字列の一致、混入ファイルの有無、テスト green、タグ付け手順）。リリース判断のときに手動で使う。
disable-model-invocation: true
allowed-tools: ["Bash", "Read", "Grep", "Glob"]
---

# /release-check

docs/13-roadmap.md：「P3（署名）まで到達すると他者と安心して共有できる状態になり、これを最初の公開リリース（v0.1）とする」。v0.1 タグを打つ前に確認する項目。

## 1. バージョン文字列の重複箇所が一致しているか

次の 2 箇所は同じバージョン文字列を持つべきである。ズレていたら、リリースするバージョンに両方揃える。

- `cmd/githive/mcp.go` の `mcp.NewServer(&mcp.Implementation{Name: "githive", Version: "..."}, nil)`
- `clients/plugins/claude/.claude-plugin/plugin.json` の `"version"` フィールド

```
grep -n 'Version:' cmd/githive/mcp.go
grep -n '"version"' clients/plugins/claude/.claude-plugin/plugin.json
```

両者が一致しない場合、また `go.mod`／CLI の `--version` 出力があればそれも含め、リリースするバージョン番号に統一する。

## 2. 混入ファイルがないか

```
git ls-files | grep -i pycache
git status --short
```

- `__pycache__/` や `*.pyc` が `git ls-files` に出てはいけない（.gitignore に追加済みのはずだが、過去にコミットされたものが残っていないか確認する）。
- `git status --short` が意図しない変更・未追跡ファイルを含んでいないか確認する。

## 3. テストゲートが green か

`/verify-all` を実行し、全ゲート PASS を確認する（go test、spec validate、go vet、gofmt、canonical JSON lint、カバレッジ確認）。1 つでも FAIL があればリリースしない。

## 4. P3 の exit criteria を満たしているか

docs/13-roadmap.md の P3 行「台帳、SSH 署名付与、`githive verify`、`whoami` が動く」を、実機（または結合テスト）で確認する。`internal/app/usersapp`、`internal/core/sign`、`internal/app/verifyapp` が揃っているか、対応する CLI サブコマンド（`cmd/githive/users.go`、`cmd/githive/verify.go`、`cmd/githive/whoami.go`）が動作するかを確認する。

## 5. タグ付け手順

すべて green を確認したら、ユーザーの明示的な承認を得てから次を行う（このスキル自身はタグ付けやリリース公開を自動実行しない。確認結果を報告し、実行してよいか尋ねる）。

```
git tag -a v0.1.0 -m "v0.1.0: MVP + 署名 (P0-P3, P5)"
git push origin v0.1.0
```

タグ名は上記バージョン文字列の一致確認で使った番号と揃える。
