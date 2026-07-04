"""
fold_task.py

docs/features/task.md の fold 規則のリファレンス実装。

fold(events) -> (meta: dict, notes: dict)

- events は封筒（envelope）の配列。本実装内で id（ULID）昇順にソートする。
- meta は meta.json 相当（status_history を含む）。
- notes は {event_id: note_dict} の辞書。

規則の要約（docs/features/task.md 準拠）:

- task.create   : 初期状態。owner 省略時は actor。チェーンの最初の
                  create だけを採用し、2 件目以降は不正として無視する
                  （issue.create と同じ扱いをここでも適用する。
                  docs に明記はないが issue の規則と対称に扱うのが自然
                  という判断。詳細は最終報告の曖昧点を参照）。
- task.edit     : title/body/due/priority を LWW 上書き。
- task.status   : 遷移し status_history に追記する。note があれば
                  notes にも追加する。不正な遷移は無視する。
- task.reassign : owner を LWW 上書き。
- task.note     : notes に追記する。
- task.link     : links 集合に適用する。
- task.checkpoint: fold は常に無視する。

ステータス機械（docs/features/task.md「ステータス機械」節）:

    todo -> doing -> review -> done
            doing -> blocked -> doing
    任意 -> cancelled

done と cancelled が終端。再開は todo への遷移イベントで表す
（終端からの todo 遷移も許可する＝再オープンに相当）。
"""

from __future__ import annotations

from typing import Optional

from events_common import sort_events

STATUSES = ("todo", "doing", "review", "done", "blocked", "cancelled")

_ALLOWED_TRANSITIONS = {
    ("todo", "doing"),
    ("doing", "review"),
    ("review", "done"),
    ("doing", "blocked"),
    ("blocked", "doing"),
}


def _is_valid_transition(frm: Optional[str], to: str) -> bool:
    if to not in STATUSES:
        return False
    if frm is None:
        return True
    if to == "cancelled":
        # 任意 -> cancelled は常に許可する。
        return True
    if to == "todo":
        # 再開（終端 done/cancelled や blocked からの todo 復帰含む）を許可する。
        return frm != "todo"
    return (frm, to) in _ALLOWED_TRANSITIONS


def fold(events: list[dict]) -> tuple[Optional[dict], dict]:
    ordered = sort_events(events)

    meta: Optional[dict] = None
    notes: dict[str, dict] = {}
    links: list[dict] = []
    status_history: list[dict] = []

    created = False

    for ev in ordered:
        kind = ev.get("kind")
        data = ev.get("data", {})
        ts = ev.get("ts")
        actor = ev.get("actor")
        eid = ev.get("id")

        if kind == "task.checkpoint":
            continue

        if kind == "task.create":
            if created:
                continue
            created = True
            owner = data.get("owner") or actor
            status_history = [{"to": "todo", "by": actor, "ts": ts}]
            meta = {
                "id": ev.get("entity"),
                "title": data.get("title", ""),
                "status": "todo",
                "owner": owner,
                "created_by": actor,
                "created_at": ts,
                "updated_at": ts,
                "status_history": list(status_history),
                "links": [],
            }
            if "body" in data:
                meta["body"] = data["body"]
            if "due" in data:
                meta["due"] = data["due"]
            if "priority" in data:
                meta["priority"] = data["priority"]
            continue

        if not created:
            continue

        if kind == "task.edit":
            if "title" in data:
                meta["title"] = data["title"]
            if "body" in data:
                meta["body"] = data["body"]
            if "due" in data:
                meta["due"] = data["due"]
            if "priority" in data:
                meta["priority"] = data["priority"]
            meta["updated_at"] = ts
            continue

        if kind == "task.status":
            to = data.get("to")
            frm = meta.get("status")
            if to is None or not _is_valid_transition(frm, to):
                continue
            meta["status"] = to
            meta["updated_at"] = ts
            status_history.append({"to": to, "by": actor, "ts": ts})
            meta["status_history"] = list(status_history)
            note_body = data.get("note")
            if note_body:
                notes[eid] = {
                    "id": eid,
                    "author": actor,
                    "ts": ts,
                    "body": note_body,
                }
            continue

        if kind == "task.reassign":
            owner = data.get("owner")
            if owner is not None:
                meta["owner"] = owner
                meta["updated_at"] = ts
            continue

        if kind == "task.note":
            notes[eid] = {
                "id": eid,
                "author": actor,
                "ts": ts,
                "body": data.get("body", ""),
            }
            meta["updated_at"] = ts
            continue

        if kind == "task.link":
            rel = data.get("rel")
            lid = data.get("id")
            remove = data.get("remove", False)
            if remove:
                links[:] = [l for l in links if not (l["rel"] == rel and l["id"] == lid)]
            else:
                if not any(l["rel"] == rel and l["id"] == lid for l in links):
                    links.append({"rel": rel, "id": lid})
            meta["links"] = list(links)
            meta["updated_at"] = ts
            continue

        # 未知の kind は無視する（forward compatibility）。

    return meta, notes
