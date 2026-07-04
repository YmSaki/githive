# 機能仕様：issue 管理

## 目的

バグ報告、機能要望、議論すべき論点を、担当者とステータスを持つ記録として管理する。

## ref とツリー

```
refs/projects/issue/<issue-id>        # issue-id は ULID

tree:
  meta.json
  body.md
  comments/
    <event-id>.md
```

### meta.json

```json
{
  "id": "01j8x0a2b3c4d5e6f7g8h9j0ka",
  "title": "sync が Windows でパスを壊す",
  "status": "in_progress",
  "labels": ["bug", "windows"],
  "assignees": ["yuumiya"],
  "created_by": "yuumiya",
  "created_at": "2026-07-04T10:00:00.000Z",
  "updated_at": "2026-07-04T12:34:56.789Z",
  "closed_at": null,
  "comment_count": 2,
  "links": [{ "rel": "task", "id": "01j8x..." }]
}
```

### comments/<event-id>.md

```markdown
---
author: yuumiya
ts: 2026-07-04T12:34:56.789Z
event: 01j8xq4d3nbz9k7w2m5e8h1t6a
---
再現しました。パス区切りの正規化漏れです。
```

## ステータス機械

`open -> in_progress -> resolved -> closed` を基本とし、`archived` 以外の任意の状態から `closed` と `open`（再オープン）へ遷移できる。
`archived` は closed の後にのみ遷移でき、一覧の既定表示から外れる。
`archived` からの遷移は `open`（復活）のみ許す。
不正な遷移イベントは fold 時に無視する（エラーにせず、検証コマンドで警告する）。

## イベント定義

| kind | data | fold 規則 |
|------|------|-----------|
| issue.create | title, body, labels[], assignees[] | 初期状態を設定。チェーンの最初のイベントに限る |
| issue.edit | title?, body? | 与えられたフィールドを上書き（LWW） |
| issue.status | from, to | ステータス機械に従い to へ遷移。from は監査用で fold には使わない |
| issue.comment | body, reply_to? | comments に event-id をキーとして追加 |
| issue.label | add[], remove[] | ラベル集合に適用（remove を先に、add を後に） |
| issue.assign | add[], remove[] | 担当者集合に適用 |
| issue.link | rel, id, remove? | links 集合に適用。rel は task / issue / chat |
| issue.checkpoint | state, count, hash | 読み出し起点（[03](../03-sync-and-concurrency.md)） |

## CLI（対応コマンドの要約、正式仕様は [10](../10-cli-spec.md)）

```
githive issue new --title <t> [--body|-F file] [--label ...] [--assign ...]
githive issue list [--status ...] [--label ...] [--assignee ...] [--json]
githive issue show <id> [--json]
githive issue comment <id> [-m msg | -F file]
githive issue status <id> <to>
githive issue edit <id> [--title] [--body]
githive issue label <id> [--add ...] [--remove ...]
githive issue assign <id> [--add ...] [--remove ...]
githive issue link <id> --task <task-id>
```

## 素の git での読み方

```
git for-each-ref refs/projects/issue                     # 一覧
git show refs/projects/issue/<id>:meta.json              # 状態
git show refs/projects/issue/<id>:body.md                # 本文
git log --format='%s' refs/projects/issue/<id>           # 履歴の要約
ls-tree で comments/ を列挙し show で読む                  # コメント
```

## 設計判断

- コメントの編集・削除は `issue.comment` の再発行ではなく、同じ event-id を `data.supersedes` で指す新コメントイベントで表す。fold は supersedes 先を置換表示扱いにする。原文はイベントログに残る。
- issue 間の重複クローズは `issue.status` + `issue.link (rel=issue)` の組で表現し、専用イベントは作らない。
- 番号（#123）は持たない。短縮 ULID とタイトルで参照する（[02](../02-data-model.md)）。
