# 機能仕様：task 管理

## 目的

「誰が、何を、いつまでに」を単位とする作業項目を管理する。
issue が「議論と決定の記録」であるのに対し、task は「実行の記録」である。
git のユーザー名（users 台帳のユーザー名）に紐づき、時系列でステータスを更新していく。

## ref とツリー

```
refs/projects/task/<task-id>          # task-id は ULID

tree:
  meta.json
  body.md
  notes/
    <event-id>.md
```

### meta.json

```json
{
  "id": "01j8xt5e6f7g8h9j0k1a2b3c4d",
  "title": "sync のパス正規化を修正する",
  "status": "doing",
  "owner": "yuumiya",
  "created_by": "yuumiya",
  "due": "2026-07-10",
  "priority": "high",
  "created_at": "2026-07-04T10:00:00.000Z",
  "updated_at": "2026-07-04T12:00:00.000Z",
  "status_history": [
    { "to": "todo",  "by": "yuumiya", "ts": "2026-07-04T10:00:00.000Z" },
    { "to": "doing", "by": "yuumiya", "ts": "2026-07-04T12:00:00.000Z" }
  ],
  "links": [{ "rel": "issue", "id": "01j8x..." }]
}
```

`status_history` は fold が自動生成する要約であり、時系列のステータス変遷を専用ツールなしで一目で読めるようにするためにある。

## ステータス機械

```
todo -> doing -> review -> done
        doing -> blocked -> doing
任意   -> cancelled
```

`done` と `cancelled` が終端。再開は `todo` への遷移イベントで表す。
終端状態から `cancelled` への遷移イベントは不正遷移として無視する（fold の基本則どおりエラーにはしない）。

## イベント定義

| kind | data | fold 規則 |
|------|------|-----------|
| task.create | title, body, owner, due?, priority? | 初期状態。owner 省略時は actor。チェーンの最初のイベントに限り、2 件目以降の create は無視する |
| task.edit | title?, body?, due?, priority? | LWW 上書き |
| task.status | from, to, note? | 遷移し status_history に追記。note があれば notes にも追加 |
| task.reassign | owner | owner を上書き（LWW） |
| task.note | body | notes に追記 |
| task.link | rel, id, remove? | links 集合に適用 |
| task.checkpoint | state, count, hash | 読み出し起点 |

## ユーザーとの紐づけ

`owner` は users 台帳のユーザー名である。
`githive task list --mine` は、git config の `githive.user`（未設定なら署名鍵から台帳を逆引き）で自分を特定する。
どちらでも解決できない場合はエラーになる（[02](../02-data-model.md) の actor 解決規則）。
Agent も同じ仕組みで自分の task を特定する。Agent 用ユーザーの扱いは [users.md](users.md) を参照。

## CLI

```
githive task new --title <t> [--owner u] [--due date] [--priority p] [--issue id]
githive task list [--mine] [--owner u] [--status ...] [--json]
githive task show <id> [--json]
githive task status <id> <to> [-m note]
githive task note <id> -m <note>
githive task reassign <id> --owner <u>
```

## 素の git での読み方

```
git for-each-ref refs/projects/task
git show refs/projects/task/<id>:meta.json     # status_history 込みで読める
git log --format='%s' refs/projects/task/<id>
```

## 設計判断

- サブタスクは v1 では持たない。`task.link (rel=task)` で関連付けだけ表現し、階層は作らない。階層はビュー側（CLI/TUI）の解釈で足りるか運用で確認してから決める。
- 見積り・実績時間のフィールドは持たない。必要になれば data のフィールド追加（v1 内互換）で足す。
- issue と task の双方向リンクは、それぞれの側の link イベントで独立に張る。片側だけでも壊れない（表示時に相手側の存在を確認する）。
