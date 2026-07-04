#!/usr/bin/env bash
# Enforces docs/02-data-model.md / docs/13-roadmap.md「手戻りリスク」:
# canonical JSON の実装は internal/core/event に一元化し、それ以外の本体
# コード（テストや spec/ の参照実装を除く）から encoding/json を直接 import
# してはならない。
set -euo pipefail

cd "$(dirname "$0")/.."

violations=$(grep -rl '"encoding/json"' --include='*.go' internal cmd 2>/dev/null \
  | grep -v '^internal/core/event/' \
  | grep -v '_test\.go$' \
  || true)

if [ -n "$violations" ]; then
  echo "FAIL: encoding/json is imported outside internal/core/event:"
  echo "$violations" | sed 's/^/  /'
  echo
  echo "Use internal/core/event.Encode/Decode instead (see docs/02-data-model.md canonical JSON)."
  exit 1
fi

echo "PASS: encoding/json usage is confined to internal/core/event (+ tests)."
