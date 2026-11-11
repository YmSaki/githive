---
name: githive-dogfood
description: このリポジトリの refs/projects/ に githive で issue・task・chat を書くとき（dogfooding）の手順。クラウドセッションから書き込みを届ける中継の使い方を含む。
---

# githive の dogfooding（Phase 0）

githive 自身の開発を、このリポジトリの refs/projects/ で管理する。
Phase 0 では **GitHub Issues が正本** で、githive には試用として書く。使いにくい点や不具合を見つけたら、GitHub issue に `dogfood` ラベルで起票する。

## 準備

```bash
go build -o "$HOME/.local/bin/githive" ./cmd/githive   # PATH に無ければ
githive whoami                                          # actor（git の user.email）を確認
git fetch origin '+refs/projects/*:refs/githive-remote/*'
```

- `githive init` を初めて使う clone で実行する。PR #167 がマージされるまでは、init の前に上の fetch を必ず行う（リモートに meta/config があるのに第 2 ルートを作る #101 を避けるため）。
- Phase 0 の書き込みは未署名でよい。

## 書き込み

クラウドセッションの git プロキシは refs/projects/* への push を拒否する（作業ブランチだけ通る）。そのため書き込みは常に `--no-sync` を付ける。

```bash
githive --no-sync task new --title "..." --issue <issue id>
githive --no-sync issue comment <id> -m "..."
```

## 届ける（中継）

1. `bash scripts/githive-relay-pack.sh` を実行する。未 push の ref を base64 の bundle にして標準出力に書く（送るものが無ければ何も出さない）。
2. 出力を入力 `bundle` に渡し、GitHub MCP の `actions_run_trigger`（method `run_workflow`、workflow_id `githive-relay.yml`、ref `main`）で起動する。
3. 実行が終わったら `git fetch origin '+refs/projects/*:refs/githive-remote/*'` のあと `githive sync` で取り込む（push するものが無ければ push は発生しない）。

同じ bundle を 2 回送っても結果は変わらない。workflow は refs/projects/ 以外の ref を受け付けない。

## 人間の手元から

push できる環境では中継は不要で、通常どおり `githive sync` を使う。
