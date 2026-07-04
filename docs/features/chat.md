# 機能仕様：chat 管理

## 目的

スレッド単位の会話ログを記録する。
リアルタイム性は目指さず、push/fetch の周期で同期する非同期掲示板として設計する（[00](../00-vision.md) の非目標）。
issue との違いは、ステータスや担当者を持たず、決定を目的としない自由な会話の場であることにある。

## ref とツリー

```
refs/projects/chat/<thread-id>        # thread-id は ULID

tree:
  meta.json
  messages/
    <event-id>.md
```

### meta.json

```json
{
  "id": "01j8xw1a2b3c4d5e6f7g8h9j0k",
  "title": "リリース手順の相談",
  "status": "open",
  "created_by": "yuumiya@example.com",
  "created_at": "2026-07-04T10:00:00.000Z",
  "updated_at": "2026-07-04T15:00:00.000Z",
  "message_count": 12,
  "participants": ["yuumiya@example.com", "dev-agent-01@example.com"]
}
```

`participants` は投稿実績のある actor の集合で、fold が自動集計する。

### messages/<event-id>.md

issue のコメントと同じ形式（front matter + 本文）。
ULID ファイル名の辞書順が投稿順なので、`ls` の並びがそのまま会話順になる。

## イベント定義

| kind | data | fold 規則 |
|------|------|-----------|
| chat.create | title, body? | スレッド作成。body があれば最初のメッセージを兼ねる |
| chat.post | body, reply_to? | messages に追記 |
| chat.edit_meta | title?, status? | LWW 上書き。status は open / archived |
| chat.checkpoint | state, count, hash | 読み出し起点 |

メッセージの編集・削除は issue のコメントと同じく `supersedes` 方式で表す。

## メンションと通知

本文中の `@username` / `@group` をメンションとする。
CLI は投稿時に本文を走査し、設定が有効なら（既定 true）メンション先を targets にした notify.post を併発する。
メンション解決は投稿時のスナップショットで行い、後からグループ構成が変わっても遡及しない。

## CLI

```
githive chat new --title <t> [-m first-message]
githive chat list [--status open] [--json]
githive chat show <id> [--tail n] [--json]
githive chat post <id> -m <body> [--reply-to event-id]
githive chat archive <id>
```

`githive chat show <id> --follow` は sync を定期実行して新着を表示する（ポーリング。リアルタイムではない）。
ポーリング間隔の既定は 60 秒とする。hosted モードで間隔を詰めるとホスティングのレート制限・不正利用検知に当たるためで、forge モードでは `--interval` で短縮してよい。

## 素の git での読み方

```
git for-each-ref refs/projects/chat
git show refs/projects/chat/<id>:meta.json
git ls-tree refs/projects/chat/<id> messages/ | 順に git show
git log --format='%s' refs/projects/chat/<id>     # 「chat.post: <冒頭>」の一覧
```

## 設計判断

- チャンネル（常設の場）とスレッドを区別しない。常設の場が欲しければ archived にせず使い続ければよい。1 スレッドが肥大したらチェックポイントが読み出しを支える。
- リアクション（絵文字）は v1 では持たない。会話の合意は本文で書く。必要になれば `chat.react` イベントの追加で拡張できる（封筒形式は変更不要）。
