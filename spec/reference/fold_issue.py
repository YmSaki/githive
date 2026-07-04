"""
fold_issue.py

docs/features/issue.md の fold 規則のリファレンス実装。

fold(events) -> (meta: dict, comments: dict)

- events は封筒（envelope）の配列。ULID 順にソート済みでなくてよい
  （本実装内で id 昇順にソートする。docs/02-data-model.md の
  state = fold(sort_by_event_id(events)) に従う）。
- meta は meta.json に相当する dict。
- comments は {event_id: comment_dict} の辞書。event_id キーで
  「単純合併」する（docs/02-data-model.md「イベントの全順序と実体化」）。

規則の要約（docs/features/issue.md 準拠）:

- issue.create   : 初期化。チェーンの最初の create だけを採用する。
                   2 件目以降の create は不正として無視する。
- issue.edit     : title / body を LWW で上書き。
- issue.status   : ステータス機械に従い to へ遷移。不正遷移は無視する。
- issue.comment  : comments に id をキーとして追加する。
                   supersedes があれば、supersedes 先のコメントを
                   置換表示（superseded_by を記録）にする。
- issue.label    : remove を先、add を後に適用する集合演算。
- issue.assign   : 同上（担当者集合）。
- issue.link     : links 集合に適用する（remove:true で除去）。
- issue.checkpoint: fold は常に無視する（docs/03-sync-and-concurrency.md
                   のチェックポイントは「読み出しの近道」であり、
                   fold 結果に影響を与えてはならない。checkpoint
                   透過性は docs/14-testing.md のプロパティテスト対象）。
- create が一度も無い場合、meta は None（存在しないエンティティ）。

ステータス機械（docs/features/issue.md「ステータス機械」節）:

    open -> in_progress -> resolved -> closed
    任意 -> closed
    任意 -> open        （再オープン）
    closed -> archived

上記に無い遷移（例: archived -> open、resolved -> in_progress の逆行等）は
不正として無視する。
"""

from __future__ import annotations

from typing import Any, Optional

from events_common import sort_events

STATUSES = ("open", "in_progress", "resolved", "closed", "archived")

# 明示的に許可する遷移の集合。
_ALLOWED_TRANSITIONS = {
    ("open", "in_progress"),
    ("in_progress", "resolved"),
    ("resolved", "closed"),
    ("closed", "archived"),
}


def _is_valid_transition(frm: Optional[str], to: str) -> bool:
    if to not in STATUSES:
        return False
    if frm is None:
        # created 直後の from が無いケースは呼び出し側で扱う。
        return True
    if to == "closed":
        # 任意 -> closed は常に許可（closed 自体からの再 close は無意味だが無害）。
        return frm != "archived"
    if to == "open":
        # 任意 -> open（再オープン）。archived からの再オープンは許可しない
        # （archived は closed の後にのみ遷移でき、一覧の既定表示から外れる
        # 終端に近い状態として扱う）。
        return frm != "archived"
    return (frm, to) in _ALLOWED_TRANSITIONS


def fold(events: list[dict]) -> tuple[Optional[dict], dict]:
    ordered = sort_events(events)

    meta: Optional[dict] = None
    comments: dict[str, dict] = {}
    labels: set[str] = set()
    assignees: set[str] = set()
    links: list[dict] = []

    created = False

    for ev in ordered:
        kind = ev.get("kind")
        data = ev.get("data", {})
        ts = ev.get("ts")
        actor = ev.get("actor")
        eid = ev.get("id")

        if kind == "issue.checkpoint":
            # fold は常に無視する。
            continue

        if kind == "issue.create":
            if created:
                # チェーンの最初のイベントに限る。2 件目以降は不正として無視。
                continue
            created = True
            labels = set(data.get("labels", []) or [])
            assignees = set(data.get("assignees", []) or [])
            meta = {
                "id": ev.get("entity"),
                "title": data.get("title", ""),
                "status": "open",
                "labels": sorted(labels),
                "assignees": sorted(assignees),
                "created_by": actor,
                "created_at": ts,
                "updated_at": ts,
                "closed_at": None,
                "comment_count": 0,
                "links": [],
            }
            if "body" in data:
                meta["body"] = data["body"]
            continue

        if not created:
            # create より前に来た（＝create が存在しない）イベントは無視する。
            continue

        if kind == "issue.edit":
            if "title" in data:
                meta["title"] = data["title"]
            if "body" in data:
                meta["body"] = data["body"]
            meta["updated_at"] = ts
            continue

        if kind == "issue.status":
            to = data.get("to")
            frm = meta.get("status")
            if to is None or not _is_valid_transition(frm, to):
                # 不正な遷移イベントは fold 時に無視する。
                continue
            meta["status"] = to
            meta["updated_at"] = ts
            if to == "closed":
                meta["closed_at"] = ts
            elif to == "open":
                meta["closed_at"] = None
            continue

        if kind == "issue.comment":
            supersedes = data.get("supersedes")
            comment = {
                "id": eid,
                "author": actor,
                "ts": ts,
                "body": data.get("body", ""),
            }
            if "reply_to" in data:
                comment["reply_to"] = data["reply_to"]
            if supersedes:
                comment["supersedes"] = supersedes
                if supersedes in comments:
                    comments[supersedes]["superseded_by"] = eid
            comments[eid] = comment
            meta["comment_count"] = len(comments)
            meta["updated_at"] = ts
            continue

        if kind == "issue.label":
            for r in data.get("remove", []) or []:
                labels.discard(r)
            for a in data.get("add", []) or []:
                labels.add(a)
            meta["labels"] = sorted(labels)
            meta["updated_at"] = ts
            continue

        if kind == "issue.assign":
            for r in data.get("remove", []) or []:
                assignees.discard(r)
            for a in data.get("add", []) or []:
                assignees.add(a)
            meta["assignees"] = sorted(assignees)
            meta["updated_at"] = ts
            continue

        if kind == "issue.link":
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

    return meta, comments
