---
name: adr
description: docs/adr/ の既存番号を調べて次番号の ADR 雛形を生成する。設計と異なる実装が必要になったときに、実装より先に手動で使う。
disable-model-invocation: true
allowed-tools: ["Read", "Write", "Glob"]
---

# /adr $ARGUMENTS

CLAUDE.md の規約：「設計と異なる実装が必要になったら、先に docs/adr/ に ADR を追加して理由を残す」。**実装の前に** このスキルで ADR を書くこと。実装してから後付けで書かない。

`$ARGUMENTS` に ADR のタイトル（日本語、体言止めでなく「〜する」のような決定の要約でよい）を渡す。

## 手順

1. `docs/adr/*.md` を列挙し、既存の最大番号を調べる（4 桁ゼロ埋め、例 `0011-mcp-no-auto-sync.md`）。次番号 = 最大 + 1。
2. ファイル名は `docs/adr/<次番号>-<英語 kebab-case のスラッグ>.md`。スラッグはタイトルの内容を簡潔に表す英語にする（既存 ADR のファイル名に倣う：`0008-remote-tracking-separation.md`、`0011-mcp-no-auto-sync.md` 等）。
3. 既存 ADR の形式に倣って雛形を書く。既存ファイル（例：`docs/adr/0004-merge-by-event-union.md`、`docs/adr/0011-mcp-no-auto-sync.md`）の構成は次の節から成る。

   ```markdown
   # ADR-<次番号>: <タイトル>

   - 状態：採用 | 検討中 | 却下 | 廃止
   - 日付：<YYYY-MM-DD>

   ## 文脈

   なぜこの決定が必要になったか。既存設計（該当する docs/ の節）のどこと矛盾する、または既存設計が扱っていない状況か。

   ## 決定

   何を決めたか。具体的に、実装が従うべき規則として書く。

   ## 理由

   なぜこの選択肢を選んだか。

   ## 帰結

   この決定によって生じる制約・トレードオフ・今後の運用上の注意。

   ## 退けた代替案

   検討したが採らなかった選択肢と、採らなかった理由。
   ```

4. 状態は基本「採用」とする（実装前に書く運用のため、検討中で止めない）。日付は今日の日付。
5. 生成後、関連する docs/ 本文（例：docs/02-data-model.md、docs/13-roadmap.md 等）に新 ADR への参照リンクを足す必要がないか確認する。既存 ADR が本文中に `[ADR-0008](adr/0008-remote-tracking-separation.md)` の形でリンクされている箇所があるパターンに倣う。

このスキルは ADR 本文の生成までを行う。実装そのものは ADR がユーザーに承認された後、別途進める。
