---
name: merge-pr
description: ユーザーがマージを承認した後に使う。PR番号を1つ受け取り、squashマージ、ローカルmainの同期、ブランチ・worktreeの後片付けまでを固定手順で行う。マージの是非を判断するスキルではない（それは /watch-pr の役目）。
disable-model-invocation: true
allowed-tools: ["Bash"]
---

# /merge-pr <PR番号>

**前提: ユーザーが明示的にこのPRのマージを承認済みであること。** このスキル自体はマージすべきかどうかを判断しない。まだ承認を得ていなければ、先に `/watch-pr` の結果を報告してユーザーに聞くこと。

## gh コマンド

`GH=$(command -v gh || echo "/c/Program Files/GitHub CLI/gh.exe")` で検出し、以降 `"$GH" ...` の形で呼ぶ（PATHにあればそれを優先、無ければWindows Git Bash向けのフルパスにフォールバック）。

## 手順（この順で。省略しない）

1. **cwdを確認する**: `pwd` を実行し、リポジトリのプライマリ working directory（`git worktree list` で末尾に `[main]` or ブランチ名だけが付き、パスが `.claude/worktrees/` を含まない行）にいることを確認する。fork/subagentが作った worktree 内に `cd` した名残でここにいないことがある（過去に実際に起きた事故）。違う場所にいたら `cd` で戻ってからでないと以降の手順を実行しない。
2. **マージ可能性の最終確認**: `"$GH" pr view <番号> --repo YmSaki/githive --json mergeable,mergeStateStatus,state -q '.mergeable, .mergeStateStatus, .state'` で `MERGEABLE` / `CLEAN` / `OPEN` を確認する。それ以外ならマージせず理由を報告する。
3. **マージ**: `"$GH" pr merge <番号> --repo YmSaki/githive --squash --delete-branch`
4. **マージ確認**: `"$GH" pr view <番号> --repo YmSaki/githive --json state,mergedAt -q '.state, .mergedAt'` が `MERGED` であることを確認する。紐づくissueがあれば（PR本文の `Closes #N`）`"$GH" issue view <N> --repo YmSaki/githive --json state -q .state` で `CLOSED` になっていることも確認する。
5. **ローカルmainの同期**（必ず手順1で確認したプライマリworking directoryで実行）:
   ```
   git checkout main
   git pull origin main
   ```
6. **ローカルブランチの後片付け**:
   - `git branch -d <ブランチ名>` を試す。squashマージ後は履歴上の祖先関係が無くなるため `error: not fully merged` で失敗するのが正常——その場合のみ `git branch -D <ブランチ名>` で強制削除する（手順4でMERGED確認済みなので安全）。
   - fork の worktree 経由で作業した場合、`git worktree list` で対象worktreeがまだ残っていないか確認し、残っていれば `git worktree remove <path> --force`。既に自動で消えていることも多い（fork終了時に自動クリーンアップされる）。
   - fork が使った worktree 由来の孤立ブランチ（`worktree-agent-*` のような名前、`git branch -a` で見つかる）が残っていれば `git branch -D` で消す。
7. **リモート追跡参照の掃除**: `git fetch --prune origin`
8. **最終確認**: `git status --short`（空であること）、`git branch -a`（不要なローカルブランチが残っていないこと）を報告する。

## 複数PRを続けてマージするとき

1つ処理し終える（手順8まで完了）たびに次のPRに進む。並行してマージしない（手順5のmain同期がPR間で競合しないようにするため、直列でよい）。
