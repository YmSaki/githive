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

ULID_RE = re.compile(r"^[0-9a-hjkmnp-tv-z]{26}$")


def is_valid_ulid(s: str) -> bool:
    return bool(ULID_RE.match(s))


def sort_events(events: Iterable[dict]) -> list[dict]:
    """イベント列を id（ULID）の辞書順でソートして返す。

    docs/02-data-model.md:
        state = fold(sort_by_event_id(events))
    「イベント ID（ULID）の辞書順を全順序とする」に従う。
    同一 id のイベントが重複して渡された場合も安定ソートで順序を保つ
    （通常は起きない想定だが、event-union マージでの重複排除は
    呼び出し側の責務とする）。
    """
    return sorted(events, key=lambda e: e["id"])


def envelope_kind_matches(event: dict, prefix: str) -> bool:
    return event.get("kind", "").startswith(prefix + ".")
