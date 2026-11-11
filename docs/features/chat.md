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

`participants` は投稿実績のある actor の集合で、fold が自動集計する。作成者（`created_by`）は投稿の有無に関わらず常に含まれる。

### messages/<event-id>.md

issue のコメントと同じ形式（front matter + 本文）。
ULID ファイル名の辞書順が投稿順なので、`ls` の並びがそのまま会話順になる。

## イベント定義

| kind | data | fold 規則 |
|------|------|-----------|
| chat.create | title, body? | スレッド作成。body があれば最初のメッセージを兼ねる |
| chat.post | body, reply_to? | messages に追記 |
| chat.edit_meta | title?, status? | LWW 上書き。status は open / archived |
| chat.checkpoint | state（meta/collections/scratch）, event_count, head, max_event_id | 読み出し起点（[ADR-0013](../adr/0013-checkpoint-envelope-convention.md)） |

メッセージの編集・削除は issue のコメントと同じく `supersedes` 方式で表す。

## メンションと通知

本文中の `@username` / `@group` をメンションとする。名前の形式は users 台帳と同じ `^[a-z0-9][a-z0-9-]{1,38}$` であり、これに合わない `@...`（メールアドレスの一部を含む）はメンションとみなさない。
投稿時に本文を走査し、`githive.notify.mentions`（既定 true）が有効なら、メンション先を targets にした notify.post を併発する。判定と文言は app 層の `internal/app/notifyapp.NotifyMentions` に 1 箇所だけ置き、CLI と MCP の両方がこれを呼ぶ。
台帳に存在しない名前は「メンションではない」として黙って無視する。投稿者自身への通知は行わない。
メンション解決は投稿時のスナップショットで行い、後からグループ構成が変わっても遡及しない。

一覧は既定で open と archived の両方を含め、ID 順で表示する。open のみを見る場合は `chat list --status open` を指定する。issue の既定一覧が archived を除外することとは別の規則である。

## CLI

```
githive chat new --title <t> [-m first-message]
githive chat list [--status open] [--json]
githive chat show <id> [--tail n] [--json]
githive chat post <id> -m <body> [--reply-to event-id]
githive chat archive <id>
githive chat edit-meta <id> [--title t] [--status s]
```

`githive chat show <id> --follow`（sync を定期実行して新着を表示するポーリング表示。リアルタイムではない）は**未実装**（P6 TUI と同時期に検討する）。

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

## 時計ずれと create の保護

新しい変更 ID が既知の非 checkpoint イベントの最大 ID 以下なら、通常の追記は `clock_skew` で保存前に拒否する。生の履歴の create より前の変更や異なる複数 create は `event_order_violation` として読み取り・同期を拒否し、fsck/verify で報告する。本文や作成者を黙って失うことを避けるための境界検査であり、fold や既存の履歴は変更しない（[ADR-0026](../adr/0026-reject-lossy-event-order.md)）。

post の reply_to / supersedes は、このスレッドの chat.post イベントの完全 ID または一意な 8 文字以上の前方一致を受け付ける。大文字は小文字の完全 ID に正規化する。別スレッドや別種別のイベントは参照できず、未知・曖昧な入力では投稿しない。置換済みの原文も履歴から参照できる。
