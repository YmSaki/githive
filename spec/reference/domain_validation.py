"""Fail-closed intake rules, separate from unchanged fold reducers (ADR-0027)."""
from __future__ import annotations

import re
from events_common import sort_events

# Match Go strings.Fields/unicode.IsSpace, including Unicode spaces but not
# Python str.split's additional U+001C..U+001F separators.
_KEY_FIELDS = re.compile(r"[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+")


def validate_users_key_order(events: list[dict]) -> None:
    """Reject ignored registrations/revocations without rewriting legacy data."""
    users: dict[str, dict[str, str]] = {}
    for event in sort_events(events):  # canonical identity conflicts are first
        kind = event.get("kind")
        data = event.get("data", {})
        username = data.get("username", "")
        if kind == "users.user_set":
            if isinstance(username, str) and username:
                users.setdefault(username, {})
            continue
        if kind not in ("users.key_add", "users.key_revoke"):
            continue
        if not isinstance(username, str) or username not in users:
            raise ValueError("event_order_violation: key operation must follow user_set")
        keys = users[username]
        pub = data.get("pub")
        fields = [part for part in _KEY_FIELDS.split(pub) if part] if isinstance(pub, str) else []
        if not fields:
            raise ValueError("event_order_violation: pub must name a key")
        material = " ".join(fields[:2])
        registered = keys.get(material)
        if kind == "users.key_add":
            if registered is not None and registered != pub:
                raise ValueError("event_order_violation: duplicate key material with a different pub")
            keys[material] = pub
        elif registered is None:
            raise ValueError("event_order_violation: revoke must follow the same user's key_add")
        elif registered != pub:
            raise ValueError("event_order_violation: revoke pub differs from registered pub")
