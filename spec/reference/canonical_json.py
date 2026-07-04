"""
canonical_json.py

githive の canonical JSON エンコーダ（リファレンス実装）。

規則は docs/02-data-model.md の「canonical JSON」節に従う。

- キーは Unicode コードポイント順にソートする。
- 数値は最短表現とする。
- 文字列は必要最小限のエスケープとする。
- インデントは 2 スペース、末尾に改行 1 つ。
- 文字コードは UTF-8、改行は LF。
- 非 ASCII 文字はエスケープしない（ensure_ascii=False 相当）。

外部依存なし（標準ライブラリのみ）。

将来 Go 実装（encoding/json ベースの独自 marshaler）が、
このモジュールの出力と byte 単位で一致することを golden テストで確認する。
"""

from __future__ import annotations

import math
from typing import Any


class CanonicalEncodeError(ValueError):
    """canonical JSON にエンコードできない値が渡されたときに送出する。"""


def encode(obj: Any) -> bytes:
    """obj を canonical JSON にエンコードし、UTF-8 バイト列で返す。

    末尾に改行を 1 つだけ付与する。改行コードは LF 固定。
    """
    text = _encode_value(obj, indent=0)
    return (text + "\n").encode("utf-8")


def encode_str(obj: Any) -> str:
    """obj を canonical JSON 文字列として返す（末尾改行込み、str 型）。

    encode() の str 版。テストやデバッグで文字列比較したい場合に使う。
    """
    return encode(obj).decode("utf-8")


def _encode_value(obj: Any, indent: int) -> str:
    if obj is None:
        return "null"
    if obj is True:
        return "true"
    if obj is False:
        return "false"
    if isinstance(obj, str):
        return _encode_string(obj)
    if isinstance(obj, int):
        # bool は上で判定済みなのでここに来るのは真の int。
        return str(obj)
    if isinstance(obj, float):
        return _encode_float(obj)
    if isinstance(obj, dict):
        return _encode_object(obj, indent)
    if isinstance(obj, (list, tuple)):
        return _encode_array(obj, indent)
    raise CanonicalEncodeError(f"unsupported type: {type(obj)!r}")


def _encode_float(x: float) -> str:
    if math.isnan(x) or math.isinf(x):
        raise CanonicalEncodeError(f"NaN/Infinity は JSON で表現できない: {x!r}")
    if x == int(x) and abs(x) < 1e16:
        # 整数値の float は最短表現として整数形式で出す（例: 1.0 -> "1"）。
        # これは「数値は最短表現」の要請に基づく。ただし言語間の
        # float/int 型の区別は入力側（データモデル）の責務とし、
        # ここでは値としての等価性のみを保証する。
        return str(int(x))
    # repr は Python の float を最短の往復可能表現にする。
    return repr(x)


# Crockford Base32 の 26 文字だけを許す ULID の様な文字列を含め、
# 一般文字列は以下のエスケープ規則に従う。
# 必要最小限のエスケープ：制御文字、", \ のみをエスケープし、
# 非 ASCII 文字はそのまま UTF-8 で出力する（ensure_ascii=False 相当）。
_ESCAPE_MAP = {
    '"': '\\"',
    "\\": "\\\\",
    "\b": "\\b",
    "\f": "\\f",
    "\n": "\\n",
    "\r": "\\r",
    "\t": "\\t",
}


def _encode_string(s: str) -> str:
    out = ['"']
    for ch in s:
        if ch in _ESCAPE_MAP:
            out.append(_ESCAPE_MAP[ch])
        elif ord(ch) < 0x20:
            out.append(f"\\u{ord(ch):04x}")
        else:
            out.append(ch)
    out.append('"')
    return "".join(out)


def _sort_key(k: str) -> tuple:
    # Unicode コードポイント順 = Python の文字列比較そのもの
    # （str の比較はコードポイント単位で行われるため追加変換は不要）。
    return (k,)


def _encode_object(obj: dict, indent: int) -> str:
    if not obj:
        return "{}"
    keys = sorted(obj.keys(), key=_sort_key)
    inner_indent = indent + 2
    pad = " " * inner_indent
    close_pad = " " * indent
    lines = []
    for k in keys:
        if not isinstance(k, str):
            raise CanonicalEncodeError(f"object key must be str, got {type(k)!r}")
        key_str = _encode_string(k)
        val_str = _encode_value(obj[k], inner_indent)
        lines.append(f"{pad}{key_str}: {val_str}")
    return "{\n" + ",\n".join(lines) + "\n" + close_pad + "}"


def _encode_array(arr, indent: int) -> str:
    if not arr:
        return "[]"
    inner_indent = indent + 2
    pad = " " * inner_indent
    close_pad = " " * indent
    lines = [f"{pad}{_encode_value(v, inner_indent)}" for v in arr]
    return "[\n" + ",\n".join(lines) + "\n" + close_pad + "]"
