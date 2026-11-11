"""Strict JSON input and bounded Unicode values (ADR-0021)."""
from __future__ import annotations

import json
from typing import Any

MAX_JSON_DEPTH = 128


def validate_value(value: Any, depth: int = 0) -> None:
    if isinstance(value, str):
        value.encode("utf-8", errors="strict")
    elif isinstance(value, dict):
        if depth >= MAX_JSON_DEPTH:
            raise ValueError("JSON nesting exceeds 128 containers")
        for key, child in value.items():
            key.encode("utf-8", errors="strict")
            validate_value(child, depth + 1)
    elif isinstance(value, (list, tuple)):
        if depth >= MAX_JSON_DEPTH:
            raise ValueError("JSON nesting exceeds 128 containers")
        for child in value:
            validate_value(child, depth + 1)


def _unique_object(pairs: list[tuple[str, Any]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key!r}")
        result[key] = value
    return result


def _invalid_constant(value: str) -> None:
    raise ValueError(f"invalid JSON constant: {value}")


def decode_strict(raw: bytes) -> Any:
    text = raw.decode("utf-8", errors="strict")
    # Bound nesting before json.loads so its own recursion limit is irrelevant.
    depth, in_string, escaped = 0, False, False
    for char in text:
        if in_string:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == '"':
                in_string = False
        elif char == '"':
            in_string = True
        elif char in "[{":
            depth += 1
            if depth > MAX_JSON_DEPTH:
                raise ValueError("JSON nesting exceeds 128 containers")
        elif char in "]}":
            depth -= 1
    value = json.loads(text, object_pairs_hook=_unique_object, parse_constant=_invalid_constant)
    validate_value(value)
    return value
