"""
fold_workpackage.py

workpackage（エイリアス wp）機能の fold 規則のリファレンス実装。
Go 実装は internal/core/materialize/workpackage.go。

fold(events) -> (meta, body, notes)

- meta  : meta.json 相当。全フィールドを常に持ち、空集合は []、未設定は None。
- body  : body.md の生文字列（create で body 未指定なら ""）。
- notes : {event_id: {actor, body, event_id, kind, ts}}。fold で no-op に
          ならず適用されたイベントのうち、非空の note / reason / body を持つもの。

共通規則:
- 実質変化なしは no-op（updated_at を更新せず note も作らない）。
- lease_until は UTC 変換 → ミリ秒未満切り捨て → [ts, ts+7日] にクランプ。
- cancel は後続イベントを消さない。
- create はチェーン先頭（非 checkpoint）の 1 件だけを採用する。
- checkpoint は常に無視する。
"""

from __future__ import annotations

import re
from datetime import datetime, timedelta, timezone
from typing import Optional

from events_common import sort_events

_ULID = re.compile(r"^[0-9a-hjkmnp-tv-z]{26}$")
_COMMIT = re.compile(r"^([0-9a-f]{40}|[0-9a-f]{64})$")
_RFC3339 = re.compile(
    r"(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)",
    re.ASCII,  # \d は ASCII 数字のみ（Go の regexp と同じ）
)
_LINK_RELS = ("issue", "task", "chat", "workpackage")
_PRIORITIES = ("low", "normal", "high")
LEASE_MAX = timedelta(days=7)


def _is_ulid(v) -> bool:
    return isinstance(v, str) and bool(_ULID.fullmatch(v))


def _non_empty(v) -> bool:
    return isinstance(v, str) and v != ""


def _opt(v):
    return v if _non_empty(v) else None


def parse_instant(value) -> Optional[datetime]:
    """RFC 3339（オフセット可）を UTC・ミリ秒切り捨ての datetime にする。"""
    if not isinstance(value, str):
        return None
    m = _RFC3339.fullmatch(value)
    if not m:
        return None
    year, month, day, hour, minute, second, frac, offset = m.groups()
    micros = 0
    if frac:
        millis = (frac + "000")[:3]  # ミリ秒未満は切り捨て
        micros = int(millis) * 1000
    try:
        if offset == "Z":
            tz = timezone.utc
        else:
            sign = 1 if offset[0] == "+" else -1
            tz = timezone(sign * timedelta(hours=int(offset[1:3]), minutes=int(offset[4:6])))
        dt = datetime(int(year), int(month), int(day), int(hour), int(minute), int(second), micros, tzinfo=tz)
        # UTC 換算で 0001〜9999 年を外れる値は不正として扱う（Go 実装と同じ範囲）。
        return dt.astimezone(timezone.utc)
    except (ValueError, OverflowError):
        return None


def format_instant(dt: datetime) -> str:
    return dt.strftime("%Y-%m-%dT%H:%M:%S.") + f"{dt.microsecond // 1000:03d}Z"


def normalize_lease(lease_until, ts) -> Optional[str]:
    norm = parse_instant(lease_until)
    at = parse_instant(ts)
    if norm is None or at is None:
        return None
    norm = max(norm, at)
    norm = min(norm, at + LEASE_MAX)
    return format_instant(norm)


def _sorted_ids(raw, self_id) -> list:
    seen = set()
    out = []
    for item in raw if isinstance(raw, list) else []:
        if _is_ulid(item) and item != self_id and item not in seen:
            seen.add(item)
            out.append(item)
    return sorted(out)


def _link_valid(rel, lid, self_id) -> bool:
    if not isinstance(rel, str) or rel not in _LINK_RELS or not _is_ulid(lid):
        return False
    return not (rel == "workpackage" and lid == self_id)


def _sort_links(links: list) -> list:
    return sorted(links, key=lambda l: (l["rel"], l["id"]))


def fold(events: list[dict]) -> tuple[Optional[dict], str, dict]:
    meta: Optional[dict] = None
    body = ""
    notes: dict[str, dict] = {}

    def add_note(ev, text):
        notes[ev["id"]] = {
            "actor": ev.get("actor"),
            "body": text,
            "event_id": ev["id"],
            "kind": ev["kind"],
            "ts": ev.get("ts"),
        }

    def find_claim(claim_id):
        for c in meta["claims"]:
            if c["claim_id"] == claim_id:
                return c
        return None

    for ev in sort_events(events):
        kind = ev.get("kind", "")
        if kind.endswith(".checkpoint"):
            continue
        data = ev.get("data", {}) or {}
        ts = ev.get("ts")
        actor = ev.get("actor")
        eid = ev.get("id")
        entity = ev.get("entity")

        if kind == "workpackage.create":
            if meta is not None:
                continue
            priority = data.get("priority") if data.get("priority") in _PRIORITIES else "normal"
            wkind = "container" if data.get("kind") == "container" else "work"
            parent = data.get("parent_id")
            parent = parent if _is_ulid(parent) and parent != entity else None
            finding_of = None
            f = data.get("finding_of")
            if isinstance(f, dict) and wkind == "work":
                if _is_ulid(f.get("workpackage")) and _is_ulid(f.get("submit_id")) and f["workpackage"] != entity:
                    finding_of = {"submit_id": f["submit_id"], "workpackage": f["workpackage"]}
            links = []
            for l in data.get("links", []) if isinstance(data.get("links"), list) else []:
                if not isinstance(l, dict):
                    continue
                rel, lid = l.get("rel"), l.get("id")
                if _link_valid(rel, lid, entity) and not any(x["rel"] == rel and x["id"] == lid for x in links):
                    links.append({"id": lid, "rel": rel})
            body = data["body"] if isinstance(data.get("body"), str) else ""
            meta = {
                "acceptance_version": eid,
                "acceptances": [],
                "cancellations": [],
                "claims": [],
                "created_at": ts,
                "created_by": actor,
                "current_submission_id": None,
                "depends_on": _sorted_ids(data.get("depends_on"), entity),
                "finding_of": finding_of,
                "id": entity,
                "kind": wkind,
                "links": _sort_links(links),
                "parent_id": parent,
                "priority": priority,
                "submissions": [],
                "title": data.get("title", "") if isinstance(data.get("title"), str) else "",
                "updated_at": ts,
                "waivers": [],
            }
            continue

        if meta is None:
            continue

        changed = False
        if kind == "workpackage.edit":
            if _non_empty(data.get("title")) and data["title"] != meta["title"]:
                meta["title"] = data["title"]
                changed = True
            if data.get("priority") in _PRIORITIES and data["priority"] != meta["priority"]:
                meta["priority"] = data["priority"]
                changed = True
            if "parent_id" in data:
                p = data["parent_id"]
                if p is None:
                    if meta["parent_id"] is not None:
                        meta["parent_id"] = None
                        changed = True
                elif _is_ulid(p) and p != entity and p != meta["parent_id"]:
                    meta["parent_id"] = p
                    changed = True
            if isinstance(data.get("body"), str) and data["body"] != body:
                body = data["body"]
                meta["acceptance_version"] = eid
                changed = True

        elif kind == "workpackage.dependency":
            target = data.get("target")
            if _is_ulid(target) and target != entity:
                deps = meta["depends_on"]
                if data.get("remove") is True:
                    if target in deps:
                        meta["depends_on"] = [d for d in deps if d != target]
                        changed = True
                elif target not in deps:
                    meta["depends_on"] = sorted(deps + [target])
                    changed = True

        elif kind == "workpackage.claim":
            clamped = normalize_lease(data.get("lease_until"), ts)
            if clamped is not None:
                meta["claims"] = meta["claims"] + [{
                    "branch": _opt(data.get("branch")),
                    "claim_id": eid,
                    "holder": actor,
                    "lease_until": clamped,
                    "released_at": None,
                    "ts": ts,
                }]
                changed = True

        elif kind == "workpackage.renew":
            c = find_claim(data.get("claim_id"))
            if c is not None and c["holder"] == actor and c["released_at"] is None and ts <= c["lease_until"]:
                clamped = normalize_lease(data.get("lease_until"), ts)
                if clamped is not None:
                    if clamped != c["lease_until"]:
                        c["lease_until"] = clamped
                        changed = True
                    if _non_empty(data.get("branch")) and data["branch"] != c["branch"]:
                        c["branch"] = data["branch"]
                        changed = True

        elif kind == "workpackage.release":
            c = find_claim(data.get("claim_id"))
            if c is not None and c["holder"] == actor and c["released_at"] is None:
                c["released_at"] = ts
                if _non_empty(data.get("note")):
                    add_note(ev, data["note"])
                changed = True

        elif kind == "workpackage.submit":
            if meta["kind"] != "container":
                commit = data.get("commit")
                reviewers = None
                if isinstance(data.get("reviewers"), list):
                    rs = sorted({r for r in data["reviewers"] if _non_empty(r)})
                    reviewers = rs or None
                meta["submissions"] = meta["submissions"] + [{
                    "acceptance_version": data.get("acceptance_version") if isinstance(data.get("acceptance_version"), str) else None,
                    "branch": _opt(data.get("branch")),
                    "by": actor,
                    "commit": commit if isinstance(commit, str) and _COMMIT.fullmatch(commit) else None,
                    "event_id": eid,
                    "pr_url": _opt(data.get("pr_url")),
                    "reviewers": reviewers,
                    "ts": ts,
                }]
                meta["current_submission_id"] = eid
                if _non_empty(data.get("note")):
                    add_note(ev, data["note"])
                changed = True

        elif kind == "workpackage.accept":
            if meta["kind"] != "container" and _is_ulid(data.get("submit_id")):
                meta["acceptances"] = meta["acceptances"] + [
                    {"by": actor, "event_id": eid, "submit_id": data["submit_id"], "ts": ts}
                ]
                if _non_empty(data.get("note")):
                    add_note(ev, data["note"])
                changed = True

        elif kind == "workpackage.waive":
            target = data.get("target_id")
            if _is_ulid(target) and target != entity and _non_empty(data.get("reason")):
                meta["waivers"] = meta["waivers"] + [
                    {"by": actor, "event_id": eid, "reason": data["reason"], "target_id": target, "ts": ts}
                ]
                add_note(ev, data["reason"])
                changed = True

        elif kind == "workpackage.cancel":
            meta["cancellations"] = meta["cancellations"] + [{"by": actor, "event_id": eid, "ts": ts}]
            if _non_empty(data.get("reason")):
                add_note(ev, data["reason"])
            changed = True

        elif kind == "workpackage.note":
            if _non_empty(data.get("body")):
                add_note(ev, data["body"])
                changed = True

        elif kind == "workpackage.link":
            rel, lid = data.get("rel"), data.get("id")
            if _link_valid(rel, lid, entity):
                links = meta["links"]
                exists = any(l["rel"] == rel and l["id"] == lid for l in links)
                if data.get("remove") is True:
                    if exists:
                        meta["links"] = [l for l in links if not (l["rel"] == rel and l["id"] == lid)]
                        changed = True
                elif not exists:
                    meta["links"] = _sort_links(links + [{"id": lid, "rel": rel}])
                    changed = True

        # 未知の kind は無視する（forward compatibility）。
        if changed:
            meta["updated_at"] = ts

    return meta, body, notes
