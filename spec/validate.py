#!/usr/bin/env python3
"""
validate.py

githive P0 の実行可能スペックを検証するスクリプト。

実行内容:
  (a) spec/vectors 配下の全イベントを envelope スキーマ + 該当 kind のデータ
      スキーマで検証する。
  (b) spec/vectors/canonical/*.json を spec/reference/canonical_json.py で
      再計算し、expected と完全一致することを確認する。
  (b2) spec/vectors/canonical-compact/*.json を
      spec/reference/canonical_json.py の encode_compact_line で再計算し、
      expected と完全一致することを確認する（docs/02-data-model.md
      「canonical JSON」の唯一の例外: notify jsonl の圧縮形）。
  (c) spec/vectors/fold-issue/*.json, spec/vectors/fold-task/*.json,
      spec/vectors/fold-chat/*.json, spec/vectors/fold-notify/*.json,
      spec/vectors/fold-users/*.json を spec/reference/fold_issue.py,
      fold_task.py, fold_chat.py, fold_notify.py, fold_users.py で
      再計算し、それぞれの expected_* と一致することを確認する。
      イベント順をシャッフルしても同一結果になることを 3 回試行で
      確認する。
  (d) spec/vectors/ordering/*.json を spec/reference/events_common.py の
      sort_events（の ID 抽出版）で再計算し、expected_sorted と
      一致することを確認する。
  (a2) spec/vectors/schema-negative/*.json の不正イベントが envelope +
      kind スキーマのどこかで拒否されることを確認する（スキーマが何も
      拒否しない状態でも (a) は PASS してしまうため）。
  (e) 結果を PASS/FAIL のサマリで stdout に出す。

終了コード: 全 PASS なら 0、1 件でも FAIL があれば 1。

外部依存: jsonschema のみ（pip install jsonschema --break-system-packages）。
"""

from __future__ import annotations

import json
import random
import sys
from pathlib import Path

SPEC_DIR = Path(__file__).resolve().parent
SCHEMAS_DIR = SPEC_DIR / "schemas"
VECTORS_DIR = SPEC_DIR / "vectors"
REFERENCE_DIR = SPEC_DIR / "reference"

sys.path.insert(0, str(REFERENCE_DIR))

try:
    from jsonschema import Draft202012Validator
except ImportError:
    print("FATAL: jsonschema がインストールされていません。")
    print("  pip install jsonschema --break-system-packages")
    sys.exit(1)

import canonical_json as cj
import fold_chat
import fold_issue
import fold_notify
import fold_task
import fold_users
from events_common import sort_events, validate_event_ids


# ---------------------------------------------------------------------------
# 結果集計
# ---------------------------------------------------------------------------

class Results:
    def __init__(self) -> None:
        self.passed: list[str] = []
        self.failed: list[tuple[str, str]] = []

    def ok(self, name: str) -> None:
        self.passed.append(name)

    def fail(self, name: str, reason: str) -> None:
        self.failed.append((name, reason))

    @property
    def total(self) -> int:
        return len(self.passed) + len(self.failed)

    def print_summary(self) -> int:
        print()
        print("=" * 70)
        print("githive spec/validate.py サマリ")
        print("=" * 70)
        for name in self.passed:
            print(f"PASS  {name}")
        for name, reason in self.failed:
            print(f"FAIL  {name}")
            print(f"      -> {reason}")
        print("-" * 70)
        print(f"合計 {self.total} 件中 PASS {len(self.passed)} 件 / FAIL {len(self.failed)} 件")
        print("=" * 70)
        if self.failed:
            print("結果: FAIL")
            return 1
        print("結果: PASS")
        return 0


# ---------------------------------------------------------------------------
# スキーマ読み込み
# ---------------------------------------------------------------------------

def load_schema(path: Path) -> dict:
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def build_validators() -> tuple[Draft202012Validator, dict[str, Draft202012Validator]]:
    envelope_schema = load_schema(SCHEMAS_DIR / "envelope.schema.json")
    envelope_validator = Draft202012Validator(envelope_schema)

    # kind の feature 部分（issue/task/notify/users/chat）ごとに
    # スキーマファイルをロードし、$defs 内の kind 名をキーにした
    # サブスキーマの Validator を作る。
    feature_files = {
        "issue": "issue.schema.json",
        "task": "task.schema.json",
        "notify": "notify.schema.json",
        "users": "users.schema.json",
        "chat": "chat.schema.json",
        "meta": "meta.schema.json",
    }
    kind_validators: dict[str, Draft202012Validator] = {}
    for feature, filename in feature_files.items():
        schema = load_schema(SCHEMAS_DIR / filename)
        defs = schema.get("$defs", {})
        for def_name, def_schema in defs.items():
            if def_name.startswith(f"{feature}."):
                # $ref 解決のため、ルートスキーマの $defs を含めた完全なコンテキストで
                # Validator を作る（def_schema 自体を $schema 込みで検証する）。
                sub = dict(def_schema)
                sub["$defs"] = defs
                kind_validators[def_name] = Draft202012Validator(sub)
    return envelope_validator, kind_validators


# ---------------------------------------------------------------------------
# (a) envelope + kind スキーマ検証
# ---------------------------------------------------------------------------

def iter_all_events() -> list[tuple[str, dict]]:
    """vectors 配下の fold-issue / fold-task / fold-chat / fold-notify /
    fold-users / meta-events の events 配列から (vector_file, event) のペアを
    列挙する。canonical/ordering ベクタはイベント封筒を持たないため対象外。
    meta-events/ は他の fold-* と違い fold 検証（run_fold_validation）の対象
    にはならない（meta/config には reducer が無いため、docs/02-data-model.md
    「meta.init イベント」）。ここでのみ、envelope + meta.schema.json による
    スキーマ検証の対象として拾われる。
    """
    out = []
    for sub in ("fold-issue", "fold-task", "fold-chat", "fold-notify", "fold-users", "meta-events"):
        d = VECTORS_DIR / sub
        for path in sorted(d.glob("*.json")):
            with open(path, encoding="utf-8") as f:
                vector = json.load(f)
            for ev in vector.get("events", []):
                out.append((str(path.relative_to(SPEC_DIR)), ev))
    return out


def run_schema_validation(results: Results) -> None:
    envelope_validator, kind_validators = build_validators()
    events = iter_all_events()
    if not events:
        results.fail("schema/no-events-found", "vectors 内にイベントが見つからない")
        return

    for src, ev in events:
        label = f"schema[{src}] id={ev.get('id')} kind={ev.get('kind')}"
        errors = sorted(envelope_validator.iter_errors(ev), key=str)
        if errors:
            results.fail(label + " (envelope)", "; ".join(str(e.message) for e in errors))
            continue

        kind = ev.get("kind")
        validator = kind_validators.get(kind)
        if validator is None:
            results.fail(label + " (kind-schema)", f"kind '{kind}' に対応するスキーマが見つからない")
            continue

        data = ev.get("data", {})
        errors = sorted(validator.iter_errors(data), key=str)
        if errors:
            results.fail(label + " (data-schema)", "; ".join(str(e.message) for e in errors))
            continue

        results.ok(label)


# ---------------------------------------------------------------------------
# (a2) スキーマの負例
# ---------------------------------------------------------------------------

def run_schema_negative_validation(results: Results) -> None:
    envelope_validator, kind_validators = build_validators()
    d = VECTORS_DIR / "schema-negative"
    paths = sorted(d.glob("*.json"))
    if not paths:
        results.fail("schema-negative/no-vectors-found", "schema-negative vectors が見つからない")
        return

    for path in paths:
        name = f"schema-negative[{path.name}]"
        with open(path, encoding="utf-8") as f:
            ev = json.load(f)["event"]
        rejected = any(True for _ in envelope_validator.iter_errors(ev))
        if not rejected:
            validator = kind_validators.get(ev.get("kind"))
            rejected = validator is None or any(True for _ in validator.iter_errors(ev.get("data", {})))
        if not rejected:
            results.fail(name, "不正なイベントがスキーマ検証を通過した")
            continue
        results.ok(name)


# ---------------------------------------------------------------------------
# (b) canonical ベクタ
# ---------------------------------------------------------------------------

def run_canonical_validation(results: Results) -> None:
    d = VECTORS_DIR / "canonical"
    paths = sorted(d.glob("*.json"))
    if not paths:
        results.fail("canonical/no-vectors-found", "canonical vectors が見つからない")
        return

    for path in paths:
        name = f"canonical[{path.name}]"
        with open(path, encoding="utf-8") as f:
            vector = json.load(f)
        try:
            actual = cj.encode_str(vector["input"])
        except Exception as e:  # noqa: BLE001
            results.fail(name, f"encode 中に例外: {e!r}")
            continue
        expected = vector["expected"]
        if actual != expected:
            results.fail(
                name,
                f"不一致\n      expected: {expected!r}\n      actual:   {actual!r}",
            )
            continue
        results.ok(name)


# ---------------------------------------------------------------------------
# (b2) canonical-compact ベクタ（圧縮形、notify jsonl 用の唯一の例外）
# ---------------------------------------------------------------------------

def run_canonical_compact_validation(results: Results) -> None:
    d = VECTORS_DIR / "canonical-compact"
    paths = sorted(d.glob("*.json"))
    if not paths:
        results.fail("canonical-compact/no-vectors-found", "canonical-compact vectors が見つからない")
        return

    for path in paths:
        name = f"canonical-compact[{path.name}]"
        with open(path, encoding="utf-8") as f:
            vector = json.load(f)
        try:
            actual = cj.encode_compact_line(vector["input"])
        except Exception as e:  # noqa: BLE001
            results.fail(name, f"encode 中に例外: {e!r}")
            continue
        expected = vector["expected"]
        if actual != expected:
            results.fail(
                name,
                f"不一致\n      expected: {expected!r}\n      actual:   {actual!r}",
            )
            continue
        results.ok(name)


# ---------------------------------------------------------------------------
# (c) fold ベクタ（issue / task / chat / notify / users）
# ---------------------------------------------------------------------------

def run_fold_validation(results: Results) -> None:
    _run_fold_dir(
        results,
        VECTORS_DIR / "fold-issue",
        fold_issue.fold,
        [("meta", "expected_meta"), ("comments", "expected_comments")],
    )
    _run_fold_dir(
        results,
        VECTORS_DIR / "fold-task",
        fold_task.fold,
        [("meta", "expected_meta"), ("notes", "expected_notes")],
    )
    _run_fold_dir(
        results,
        VECTORS_DIR / "fold-chat",
        fold_chat.fold,
        [("meta", "expected_meta"), ("messages", "expected_messages")],
    )
    _run_fold_dir(
        results,
        VECTORS_DIR / "fold-notify",
        fold_notify.fold,
        [("meta", "expected_meta"), ("posts", "expected_posts"), ("acks", "expected_acks")],
    )
    _run_fold_dir(
        results,
        VECTORS_DIR / "fold-users",
        fold_users.fold,
        [("users", "expected_users"), ("groups", "expected_groups"), ("policy", "expected_policy")],
    )


def _run_fold_dir(results: Results, dir_path: Path, fold_fn, fields: list[tuple[str, str]]) -> None:
    """1 ベクタディレクトリを検証する。fold_fn(events) は fields と同じ順序・
    個数のタプルを返す（issue/task/chat は (meta, コレクション) の 2 要素、
    notify は month 派生フィールドを含む posts に加えて Scratch 由来の acks
    を持つため (meta, posts, acks) の 3 要素、という具合に feature ごとに
    異なるため、fields で汎用化してある）。
    """
    paths = sorted(dir_path.glob("*.json"))
    if not paths:
        results.fail(f"fold/no-vectors-found[{dir_path.name}]", "vectors が見つからない")
        return

    for path in paths:
        name = f"fold[{dir_path.name}/{path.name}]"
        with open(path, encoding="utf-8") as f:
            vector = json.load(f)

        events = vector["events"]
        expected = [vector[key] for _, key in fields]

        def mismatch_reason(got: tuple, label: str) -> str | None:
            for (field_name, _), want, actual in zip(fields, expected, got):
                if actual != want:
                    return (
                        f"{label}: {field_name} 不一致\n"
                        f"      expected: {json.dumps(want, ensure_ascii=False)}\n"
                        f"      actual:   {json.dumps(actual, ensure_ascii=False)}"
                    )
            return None

        # 元の順序での fold
        try:
            got = fold_fn(events)
        except Exception as e:  # noqa: BLE001
            results.fail(name + " (original order)", f"fold 中に例外: {e!r}")
            continue

        reason = mismatch_reason(got, "original order")
        if reason is not None:
            results.fail(name + " (original order)", reason)
            continue

        # シャッフルして 3 回試行し、同一結果になることを確認する。
        shuffle_ok = True
        shuffle_reason = ""
        rng = random.Random(f"shuffle-seed-{path.name}")
        for trial in range(3):
            shuffled = events[:]
            rng.shuffle(shuffled)
            try:
                got2 = fold_fn(shuffled)
            except Exception as e:  # noqa: BLE001
                shuffle_ok = False
                shuffle_reason = f"trial {trial}: fold 中に例外: {e!r}"
                break
            reason2 = mismatch_reason(got2, f"trial {trial}")
            if reason2 is not None:
                shuffle_ok = False
                shuffle_reason = f"trial {trial}: シャッフル後の結果が期待値と不一致\n      {reason2}"
                break

        if not shuffle_ok:
            results.fail(name + " (shuffle-invariance)", shuffle_reason)
            continue

        results.ok(name)


# ---------------------------------------------------------------------------
# (d) ordering ベクタ
# ---------------------------------------------------------------------------

def run_ordering_validation(results: Results) -> None:
    """spec/vectors/ordering/*.json を events_common.sort_events で
    再計算する。sort_events は封筒（dict の "id" キー）を前提にしているため、
    生の ULID 文字列を {"id": s} に包んでソートし、ソート後に取り出す
    （spec/README.md「vectors/ の内容」、ADR なしの単純な ULID 辞書順
    ソートの検証）。
    """
    d = VECTORS_DIR / "ordering"
    paths = sorted(d.glob("*.json"))
    if not paths:
        results.fail("ordering/no-vectors-found", "ordering vectors が見つからない")
        return

    for path in paths:
        name = f"ordering[{path.name}]"
        with open(path, encoding="utf-8") as f:
            vector = json.load(f)

        input_ids = vector["input"]
        expected_sorted = vector["expected_sorted"]

        wrapped = [{"id": s} for s in input_ids]
        try:
            actual = [e["id"] for e in sort_events(wrapped)]
        except Exception as e:  # noqa: BLE001
            results.fail(name, f"sort_events 中に例外: {e!r}")
            continue

        if actual != expected_sorted:
            results.fail(
                name,
                f"不一致\n      expected: {expected_sorted!r}\n      actual:   {actual!r}",
            )
            continue
        results.ok(name)


def run_strict_json_validation(results: Results) -> None:
    from strict_json import decode_strict
    path = VECTORS_DIR / "json-decoding" / "01-strict-inputs.json"
    with open(path, encoding="utf-8") as source:
        cases = json.load(source)["cases"]
    for case in cases:
        raw = bytes.fromhex(case["hex"]) if "hex" in case else case["source"].encode("utf-8")
        try:
            decode_strict(raw)
            accepted = True
        except (ValueError, UnicodeError):
            accepted = False
        name = f"strict-json[{case['name']}]"
        if accepted != case["accepted"]:
            results.fail(name, f"accepted={accepted}, expected={case['accepted']}")
        else:
            results.ok(name)


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def run_event_identity_validation(results: Results) -> None:
    for path in sorted((VECTORS_DIR / "event-identity").glob("*.json")):
        vector = json.loads(path.read_text(encoding="utf-8"))
        name = f"event-identity[{path.name}]"
        for events in (vector["events"], list(reversed(vector["events"]))):
            try:
                validate_event_ids(events)
            except ValueError as err:
                if not vector["conflicting"] or "conflicting_event_id" not in str(err):
                    results.fail(name, str(err))
                    break
            else:
                if vector["conflicting"]:
                    results.fail(name, "conflicting event ID accepted")
                    break
        else:
            results.ok(name)


def main() -> int:
    results = Results()
    run_schema_validation(results)
    run_schema_negative_validation(results)
    run_canonical_validation(results)
    run_canonical_compact_validation(results)
    run_fold_validation(results)
    run_ordering_validation(results)
    run_strict_json_validation(results)
    run_event_identity_validation(results)
    return results.print_summary()


if __name__ == "__main__":
    sys.exit(main())
