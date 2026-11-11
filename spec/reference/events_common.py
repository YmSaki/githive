"""
events_common.py

fold_issue.py / fold_task.py が共有するユーティリティ。

- イベント列の ULID（id フィールド）順ソート（docs/02-data-model.md
  「イベントの全順序と実体化」: state = fold(sort_by_event_id(events))）。
- ULID の妥当性チェック（envelope.schema.json と同じ正規表現）。

外部依存なし。
"""

from __future__ import annotations

import re
from typing import Any, Iterable

from canonical_json import encode

ULID_RE = re.compile(r"^[0-9a-hjkmnp-tv-z]{26}$")


def is_valid_ulid(s: str) -> bool:
    return bool(ULID_RE.match(s))


def sort_events(events: Iterable[dict]) -> list[dict]:
    """イベント列を id（ULID）の辞書順でソートして返す。

    docs/02-data-model.md:
        state = fold(sort_by_event_id(events))
    「イベント ID（ULID）の辞書順を全順序とする」に従う。
    同じ id の canonical bytes が異なる場合は破損として拒否する。
    一致するコピーの入力順は維持する（和集合側で重複を除去する）。
    """
    checked = list(events)
    validate_event_ids(checked)
    return sorted(checked, key=lambda e: e["id"])


def envelope_kind_matches(event: dict, prefix: str) -> bool:
    return event.get("kind", "").startswith(prefix + ".")


def validate_event_ids(events: Iterable[dict]) -> None:
    """Reject contradictory event identities without choosing a winner (ADR-0023)."""
    seen: dict[str, bytes] = {}
    for event in events:
        raw = encode(event)
        event_id = event["id"]
        if event_id in seen and seen[event_id] != raw:
            raise ValueError(f"conflicting_event_id: {event_id}")
        seen[event_id] = raw
