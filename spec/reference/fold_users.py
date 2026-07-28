"""
fold_users.py

docs/features/users.md の fold 規則のリファレンス実装。
internal/core/materialize/users.go からの逐行移植（Go が正）。

fold(events) -> (users: dict, groups: dict, policy: dict | None)

- events は封筒（envelope）の配列。本実装内で id（ULID）昇順にソートする。
- users/registry は chat/notify と同じくシングルトンだが、単一の meta を
  持たず、3 つの独立した副資源（users・groups・policy）を持つ
  (users.go の package docstring「State.Meta is unused here」に対応)。
  そのため fold は meta を返さず、3 つの値をそのまま返す。
- users は {username: user_dict} の辞書（users/<name>.json 相当）。
- groups は {name: group_dict} の辞書（groups/<name>.json 相当）。
- policy は policy.json 相当の dict、または users.policy_set が一度も
  無ければ None（users.go の Policy() に対応。全置換 LWW 値であり、
  部分編集イベントにはしない）。

規則の要約（docs/features/users.md 準拠、users.go に一致させる）:

- users.user_set    : username が空なら無視する。既存ユーザーがいなければ
                      既定値（kind="human", status="active", 空配列群）で
                      初期化してから、fields の各キーを上書きする（LWW）。
                      ただし "keys" と "username" は fields からの上書きを
                      無視する（keys は key_add/key_revoke 専用、username は
                      イベントの username で固定する）。
- users.key_add     : username/pub が空なら無視する。username が
                      user_set されたことのないユーザーなら無視する
                      （自動作成しない）。同じ pub が既にあれば no-op
                      （revoked 済みでも再アクティブ化しない）。それ以外は
                      末尾に {pub, added_at, revoked_at: None} を追加する。
- users.key_revoke  : username/pub が空、またはユーザー未登録なら無視する。
                      pub が一致しかつ revoked_at が None の鍵の
                      revoked_at をイベントの ts にする（削除しない）。
- users.group_set   : name が空なら無視する。既存グループとマージせず、
                      {name, members, description?} で完全に置き換える。
- users.group_remove: name が空なら無視する。groups から該当キーを削除
                      する（無ければ no-op）。
- users.policy_set  : rules/default を丸ごと置き換える（部分編集ではない
                      whole-object replace の LWW）。
- users.checkpoint  : fold は常に無視する。
"""

from __future__ import annotations

from typing import Any, Optional

from events_common import sort_events


def fold(events: list[dict]) -> tuple[dict, dict, Optional[dict]]:
    ordered = sort_events(events)

    users: dict[str, dict] = {}
    groups: dict[str, dict] = {}
    policy: Optional[dict] = None

    for ev in ordered:
        kind = ev.get("kind")
        data = ev.get("data", {})
        ts = ev.get("ts")

        if kind == "users.checkpoint":
            # fold は常に無視する。
            continue

        if kind == "users.user_set":
            username = data.get("username")
            if not username:
                continue
            fields = data.get("fields") or {}
            user = users.get(username)
            if user is None:
                user = {
                    "username": username,
                    "kind": "human",
                    "status": "active",
                    "emails": [],
                    "roles": [],
                    "keys": [],
                }
            for k, v in fields.items():
                if k in ("keys", "username"):
                    continue  # keys are managed only via key_add/key_revoke
                user[k] = v
            user["username"] = username
            users[username] = user
            continue

        if kind == "users.key_add":
            username = data.get("username")
            pub = data.get("pub")
            if not username or not pub:
                continue
            user = users.get(username)
            if user is None:
                continue  # key_add on a user that was never user_set is ignored
            keys = user.get("keys") or []
            if any(k.get("pub") == pub for k in keys):
                continue  # already present, no-op
            keys = keys + [{"pub": pub, "added_at": ts, "revoked_at": None}]
            user["keys"] = keys
            continue

        if kind == "users.key_revoke":
            username = data.get("username")
            pub = data.get("pub")
            if not username or not pub:
                continue
            user = users.get(username)
            if user is None:
                continue
            for k in user.get("keys") or []:
                if k.get("pub") == pub and k.get("revoked_at") is None:
                    k["revoked_at"] = ts
            continue

        if kind == "users.group_set":
            name = data.get("name")
            if not name:
                continue
            group: dict[str, Any] = {"name": name, "members": data.get("members")}
            if "description" in data:
                group["description"] = data["description"]
            groups[name] = group
            continue

        if kind == "users.group_remove":
            name = data.get("name")
            if not name:
                continue
            groups.pop(name, None)
            continue

        if kind == "users.policy_set":
            policy = {
                "rules": data.get("rules"),
                "default": data.get("default"),
            }
            continue

        # 未知の kind は無視する（forward compatibility）。

    return users, groups, policy
