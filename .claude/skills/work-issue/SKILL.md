---
name: work-issue
description: issue番号を1つ受け取り、実装ブランチ作成からPR作成まで一気通貫でやる。task/bugラベルの付いたissueに使う。マージはしない（/watch-pr、/merge-pr に続く）。
disable-model-invocation: true
allowed-tools: ["Bash", "Read", "Edit", "Write", "Grep", "Glob", "Agent"]
---

# /work-issue <issue番号>

引数の issue 番号（例: `/work-issue 12`）を実装し、PR を作成するところまでを実行する。マージはしない。

## gh コマンド

`gh` は素の PATH に無い環境がある。`GH="/c/Program Files/GitHub CLI/gh.exe"` として、以降 `"$GH" ...` の形で呼ぶ。

## 手順

1. **前提確認**: `git status --short` で作業ツリーがクリーンであることを確認する。汚れていれば止めて報告する（他の作業が残っている可能性がある）。`git branch --show-current` が `main` でなければ `main` に戻す（他のissueの後片付け漏れの可能性があるため、勝手にstashせず状況を報告してから戻す）。
2. **issue内容の取得**: `"$GH" issue view <番号> --repo YmSaki/githive --json title,body,labels` で受け入れ条件・影響範囲・テスト観点を読む。`.claude/rules/` (determinism.md / go-layering.md / spec-sync.md) に照らして実装方針を決める。関係する既存コードを読み、パターンを把握する（似た機能の既存実装を真似るのがこのリポジトリの流儀。「2例目まで複製、3例目で抽出」）。
3. **規模判断**: 実装が複数ファイルにまたがる・既存パターンの読解が要る中〜大規模なタスクなら、`Agent`（`subagent_type: "fork"`）に委任する。小さい変更（1ファイル数行）はここで直接やってよい。委任する場合、以下を必ずプロンプトに含める:
   - `gh` のフルパス呼び出し方法
   - issue本文の受け入れ条件の引用
   - 関連コードの調査結果（該当ファイル・行番号・既存パターン）
   - ブランチ名（`claude/<簡潔なスラッグ>`）
   - 検証コマンド一式（下記）を全部PASSさせること
   - コミットメッセージは日本語・実装単位で分ける
   - push後 `"$GH" pr create --repo YmSaki/githive --base main` でPR作成、本文に `Closes #<番号>` と検証結果を書く
   - 完了後の auto-pr-review 発火は待たなくてよい
4. **検証（フォークに委任した場合も、戻ってきたら自分でもう一度確認する）**:
   - `go build ./...`
   - `go test ./...`
   - `python spec/validate.py`（`jsonschema` が import できる python を優先。`python3` → `python` の順で試す）
   - `go vet ./...`
   - `gofmt -l .`
   - `bash scripts/check-canonical-json.sh`
   全部PASSしていなければ、PRを作らず修正する。
5. **PR作成後**: PR URLとissue番号を報告する。`/watch-pr <PR番号>` に進むかユーザーに確認する。

## 並列実行するとき

同時に複数issueを進める場合、**触るファイルが重ならないことを確認してから**並列化する（変更対象のファイル一覧をissue本文または調査で洗い出し、重複があれば片方が先にマージされてから他方を分岐させる）。並列で `Agent` を複数呼ぶ場合は各エージェントに `isolation: "worktree"` を指定し、同じ作業ツリーを同時にいじらせない。ファイルが重なる場合は並列化せず順番にやる（コンフリクト解消よりコンフリクト回避が安い）。
