"""
fold_chat.py

docs/features/chat.md の fold 規則のリファレンス実装。
internal/core/materialize/chat.go からの逐行移植（Go が正）。

fold(events) -> (meta: dict | None, messages: dict)

- events は封筒（envelope）の配列。ULID 順にソート済みでなくてよい
  （本実装内で id 昇順にソートする。docs/02-data-model.md の
  state = fold(sort_by_event_id(events)) に従う）。
- meta は meta.json に相当する dict。
- messages は {event_id: message_dict} の辞書。event_id キーで
  「単純合併」する（docs/02-data-model.md「イベントの全順序と実体化」）。

規則の要約（docs/features/chat.md 準拠、chat.go に一致させる）:

- chat.create    : 初期化。チェーンの最初の create だけを採用する。
                   2 件目以降の create は不正として無視する（issue.create
                   と同じ扱い）。作成者は常に participants に加わる
                   （body の有無に関わらない）。body があれば、その
                   create イベント自身の id をキーとして最初のメッセージを
                   兼ねる（docs/features/chat.md「イベント定義」）。
- chat.post      : messages に author/ts/body を追加する。reply_to は
                   キーが存在すれば型を問わず素通しする。supersedes は
                   非空文字列のときだけ適用し、supersedes 先のメッセージに
                   superseded_by を記録する。投稿者は participants に
                   加わる。
- chat.edit_meta : title は存在すれば型を問わず上書きする（LWW）。status
                   は文字列で "open" か "archived" のときだけ上書きする。
                   updated_at は（他のフィールドが実際に変わったかに関係
                   なく）常に更新する。
- chat.checkpoint: fold は常に無視する。
- create が一度も無い場合、meta は None（存在しないエンティティ）。

participants（docs/features/chat.md「meta.json」の participants 説明）:
    投稿実績のある actor の集合。chat.create と chat.post の actor を
    集合に加え、更新のたびに昇順ソートして meta["participants"] に
    書き戻す（chat.go の addChatParticipant / chatParticipants に対応）。
"""

from __future__ import annotations

from typing import Any, Optional

from events_common import sort_events


def fold(events: list[dict]) -> tuple[Optional[dict], dict]:
    ordered = sort_events(events)

    meta: Optional[dict] = None
    messages: dict[str, dict] = {}
    participants: set[str] = set()

    created = False

    def add_participant(actor: str) -> None:
        participants.add(actor)
        meta["participants"] = sorted(participants)

    for ev in ordered:
        kind = ev.get("kind")
        data = ev.get("data", {})
        ts = ev.get("ts")
        actor = ev.get("actor")
        eid = ev.get("id")

        if kind == "chat.checkpoint":
            # fold は常に無視する。
            continue

        if kind == "chat.create":
            if created:
                # チェーンの最初のイベントに限る。2 件目以降は不正として無視。
                continue
            created = True
            meta = {
                "id": ev.get("entity"),
                "title": data.get("title", ""),
                "status": "open",
                "created_by": actor,
                "created_at": ts,
                "updated_at": ts,
                "message_count": 0,
                "participants": [],
            }
            add_participant(actor)

            # body があれば、create 自身がスレッドの最初のメッセージを兼ねる
            # (docs/features/chat.md「イベント定義」)。
            body = data.get("body")
            if isinstance(body, str) and body != "":
                messages[eid] = {
                    "id": eid,
                    "author": actor,
                    "ts": ts,
                    "body": body,
                }
                meta["message_count"] = len(messages)
            continue

        if not created:
            # create より前に来た（＝create が存在しない）イベントは無視する。
            continue

        if kind == "chat.post":
            message: dict[str, Any] = {
                "id": eid,
                "author": actor,
                "ts": ts,
                "body": data.get("body", ""),
            }
            if "reply_to" in data:
                message["reply_to"] = data["reply_to"]
            supersedes = data.get("supersedes")
            if isinstance(supersedes, str) and supersedes != "":
                message["supersedes"] = supersedes
                prior = messages.get(supersedes)
                if prior is not None:
                    prior["superseded_by"] = eid
            messages[eid] = message
            meta["message_count"] = len(messages)
            add_participant(actor)
            meta["updated_at"] = ts
            continue

        if kind == "chat.edit_meta":
            if "title" in data:
                meta["title"] = data["title"]
            status = data.get("status")
            if isinstance(status, str) and status in ("open", "archived"):
                meta["status"] = status
            meta["updated_at"] = ts
            continue

        # 未知の kind は無視する（forward compatibility）。

    return meta, messages
