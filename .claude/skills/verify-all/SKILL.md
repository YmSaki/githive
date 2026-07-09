---
name: verify-all
description: githive の全品質ゲートを一括実行し、PASS/FAIL 表で報告する。フェーズ完了や push 可否を判定するときに手動で使う。
disable-model-invocation: true
allowed-tools: ["Bash"]
---

# /verify-all

githive の「テストなしのフェーズ完了は無い」（CLAUDE.md）を機械的に確認するための一括検証コマンド。
すべてのゲートを実行し、最後に PASS/FAIL 表で報告する。**1 つでも FAIL があれば、現在のフェーズは完了とみなさず、push もしない。**

## 実行するゲート（この順で実行し、失敗しても後続は止めずに全部走らせる）

1. `go test ./...`
2. `python spec/validate.py`（`jsonschema` が import できる python を使う。`python3` が Windows Store のスタブ等で `import jsonschema` に失敗する環境があるので、`python3` → `python` の順で `python -c "import jsonschema"` が通る方を選ぶこと）
3. `go vet ./...`
4. `gofmt -l .`（出力が空でなければ FAIL。フォーマット崩れのファイル一覧を報告する）
5. `bash scripts/check-canonical-json.sh`
6. カバレッジ：`go test -coverprofile=/tmp/githive-cover.out ./...` の後 `go tool cover -func=/tmp/githive-cover.out` で総カバレッジ（末尾の `total:` 行）を取得する。数値そのものが FAIL 条件にはならないが、著しく低い（例えば新規パッケージが 0% など）場合は表に注記する。

## 実行後の報告フォーマット

以下の形式で PASS/FAIL 表を出す。各行に、FAIL の場合は失敗の要約（該当パッケージ名、テスト名、diff の要点など）を添える。

```
| ゲート                              | 結果  | 備考                          |
|--------------------------------------|-------|-------------------------------|
| go test ./...                        | PASS  |                                |
| python spec/validate.py              | PASS  | 62/62                          |
| go vet ./...                         | PASS  |                                |
| gofmt -l .                           | PASS  |                                |
| scripts/check-canonical-json.sh      | PASS  |                                |
| coverage (go tool cover -func)       | 情報  | total: 78.3%                   |

総合判定: PASS（全ゲート green） / FAIL（上記のうち FAIL 参照）
```

FAIL が 1 件でもあれば、表の直後に「フェーズ完了・push 不可」と明記し、原因ファイル・修正方針の見立てを続ける。全 PASS の場合のみ「フェーズ完了条件（テストゲートの面では）を満たしている」と報告する（ドキュメント上の exit criteria 全体を満たすかどうかは別途 docs/13-roadmap.md と突き合わせること）。
