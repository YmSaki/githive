---
name: watch-pr
description: PR番号を1つ受け取り、CIとauto-pr-reviewの完走をScheduleWakeupで待ち、結果を要約報告する。blocking指摘があれば直して再pushし再監視する。マージはしない（ユーザーの判断を仰いで終わる）。
disable-model-invocation: true
allowed-tools: ["Bash", "Read", "Edit", "ScheduleWakeup"]
---

# /watch-pr <PR番号>

引数のPR番号（例: `/watch-pr 13`）のCIとauto-pr-review（`.github/workflows/auto-pr-review.yml`、`.github/workflows/ci.yml`）が完走するまで待ち、結果を要約する。**このスキルはマージを一切行わない**。マージの是非は必ずユーザーに確認する（このリポジトリの固定方針。CI・レビューが全green・blocking無しでも、マージ実行だけは毎回止まって聞く）。

## gh コマンド

`GH=$(command -v gh || echo "/c/Program Files/GitHub CLI/gh.exe")` で検出し、以降 `"$GH" ...` の形で呼ぶ（PATHにあればそれを優先、無ければWindows Git Bash向けのフルパスにフォールバック）。

## 手順

1. `"$GH" pr checks <番号> --repo YmSaki/githive` でCI・reviewの状態を確認する。
2. **まだ実行中のものがあれば**: `ScheduleWakeup` で待つ。目安の delaySeconds は、直近の実行時間の実績（このセッションでは概ね2〜6分）を踏まえて 250〜300 秒。`prompt` には「このスキル（/watch-pr <番号>）の続きから、`pr checks` の再確認から始める」ことが分かる自己完結した指示を書く（ScheduleWakeupのprompt自体がこのSKILL.mdの内容を知らない前提で、必要な手順・ghのフルパス・番号を再掲する）。`reason` には何を待っているか具体的に書く。ここで一旦ターンを終える。
3. **全部完了していれば**:
   - `"$GH" pr view <番号> --repo YmSaki/githive --comments` で auto-pr-review の sticky comment を取得する。
   - CI（test ubuntu/windows, spec-validate）が全PASSか確認する。
   - sticky comment 内の blocking / non-blocking / nit 指摘を読み、要約する。
4. **blocking指摘がある場合**:
   - 指摘内容を読み、このリポジトリの規約（`.claude/rules/`、docs/）に照らして妥当か判断する。妥当なら直接修正するか、規模が大きければ `Agent`(fork) に修正を委任する。
   - 修正をcommit・push（PRブランチに直接pushする。新しいブランチは作らない）。
   - push後、再び手順1から繰り返す（新しいCI runとreview runが走るので、その run IDを`"$GH" run list --repo YmSaki/githive --branch <ブランチ名> --limit 3`で拾ってから待つ）。
   - この修正→再監視ループは際限なく回さない。同じ種類の指摘が2回続けて出るなど収束しない兆候があれば、ループを止めてユーザーに判断を仰ぐ。
5. **blocking指摘が無い場合**: CI結果・レビュー結果（non-blocking/nitの要約、起票された後続issueがあれば番号）を報告し、「マージ可能と判断します。マージしていいですか」とユーザーに確認して終わる。**ここで `/merge-pr` を勝手に呼ばない**。

## ワークフローファイル自体を直した場合の注意

`.github/workflows/*.yml` は `pull_request` トリガーの検証上、PRブランチ側と `main` 側の内容が一致していないと実行がスキップされる（validation failed）。ワークフロー定義自体を直す必要が生じた場合は、その修正は当該PRのブランチにではなく **`main` に直接コミットしてから**、対象PRブランチに `git merge origin/main` で取り込んで再push する（`main`への直接pushは「共有状態への影響」なので一度ユーザーに一言断ってから行う）。
