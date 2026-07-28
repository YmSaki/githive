"""
fold_notify.py

docs/features/notify.md の fold 規則のリファレンス実装。
internal/core/materialize/notify.go からの逐行移植（Go が正）。

fold(events) -> (meta: dict | None, posts: dict, acks: dict)

- events は封筒（envelope）の配列。本実装内で id（ULID）昇順にソートする。
- issue/task/chat と異なり、notify/stream は create イベントを持たない
  シングルトンの追記専用ログである。meta は「エンティティが存在するか」の
  目印として、最初に受理された非 checkpoint イベントで空の dict になる
  だけで、以後もフィールドを一切持たない（chat.go の
  ensureNotifyMeta に対応。「notify.checkpoint のみ」の入力では meta は
  None のまま）。
- posts は {event_id: post_dict} の辞書（events/<month>.jsonl 相当）。
- acks は {ack_of: [actor, ...]}（昇順ソート・重複排除済み）の辞書
  （acks.json 相当）。Go 側では State.Scratch に置かれ、
  materialize.NotifyAcks で公開される値に対応する。

規則の要約（docs/features/notify.md 準拠、notify.go に一致させる）:

- notify.post      : posts に 1 件追記する。targets はそのまま素通しする。
                      title は文字列でなければ空文字扱い。body / source /
                      priority はキーが存在すれば素通しする。month は
                      ts の先頭 7 文字（"YYYY-MM"）から導出する
                      （docs/features/notify.md「ref とツリー」の
                      events/<月>.jsonl への振り分けに対応）。
- notify.ack       : ack_of が非空文字列でなければ無視する。既に同じ
                      actor が ack 済みなら no-op（重複しない）。そうで
                      なければ actor を追加し、リストを昇順ソートする。
- notify.checkpoint: fold は常に無視する。
"""

from __future__ import annotations

from typing import Any, Optional

from events_common import sort_events


def _month_of(ts: str) -> str:
    """ts の "YYYY-MM" バケットを取り出す
    (docs/features/notify.md「ref とツリー」、notify.go の monthOf)。"""
    if ts is None or len(ts) < 7:
        return ts
    return ts[:7]


def fold(events: list[dict]) -> tuple[Optional[dict], dict, dict]:
    ordered = sort_events(events)

    meta: Optional[dict] = None
    posts: dict[str, dict] = {}
    acks: dict[str, list[str]] = {}

    for ev in ordered:
        kind = ev.get("kind")
        data = ev.get("data", {})
        ts = ev.get("ts")
        actor = ev.get("actor")
        eid = ev.get("id")

        if kind == "notify.checkpoint":
            # fold は常に無視する。
            continue

        if kind == "notify.post":
            if meta is None:
                meta = {}
            post: dict[str, Any] = {
                "id": eid,
                "ts": ts,
                "actor": actor,
                "targets": data.get("targets"),
                "title": data.get("title", ""),
                "month": _month_of(ts),
            }
            if "body" in data:
                post["body"] = data["body"]
            if "source" in data:
                post["source"] = data["source"]
            if "priority" in data:
                post["priority"] = data["priority"]
            posts[eid] = post
            continue

        if kind == "notify.ack":
            if meta is None:
                meta = {}
            ack_of = data.get("ack_of")
            if not isinstance(ack_of, str) or ack_of == "":
                continue
            lst = acks.get(ack_of, [])
            if actor in lst:
                continue  # already acked, no-op
            lst = lst + [actor]
            lst.sort()
            acks[ack_of] = lst
            continue

        # 未知の kind は無視する（forward compatibility）。

    return meta, posts, acks
