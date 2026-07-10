#!/usr/bin/env bash
# PostToolUse hook (matcher: Edit|Write).
#
# When the edited/written file is a .go file, run gofmt -w on it and then
# scripts/check-canonical-json.sh over the whole repo, enforcing
# docs/02-data-model.md / docs/13-roadmap.md「手戻りリスク」: canonical JSON
# stays implemented in exactly one place (internal/core/event).
#
# Reads the PostToolUse hook JSON payload from stdin
# (https://code.claude.com/docs/en/hooks): {"tool_input": {"file_path": ...}, ...}.
# Avoids a jq dependency by parsing with python3 (already required by this
# repo for spec/validate.py), falling back to `python` if python3 is absent.
#
# PostToolUse cannot block the tool call (it already ran); exit 2 here only
# surfaces the failure to Claude via stderr so it can fix the file.

set -uo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

INPUT="$(cat)"

PY="$(pick_json_python)"

FILE_PATH="$(printf '%s' "$INPUT" | "$PY" -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print("")
    sys.exit(0)
print(data.get("tool_input", {}).get("file_path") or "")
')"

if [ -z "$FILE_PATH" ]; then
  exit 0
fi

case "$FILE_PATH" in
  *.go) ;;
  *) exit 0 ;;
esac

REPO_ROOT="$(resolve_repo_root)"
cd "$REPO_ROOT" || exit 0

STATUS=0

if command -v gofmt >/dev/null 2>&1; then
  if ! GOFMT_ERR=$(gofmt -w "$FILE_PATH" 2>&1); then
    echo "gofmt -w failed on $FILE_PATH:" >&2
    echo "$GOFMT_ERR" >&2
    STATUS=2
  fi
else
  echo "warning: gofmt not found on PATH, skipping format of $FILE_PATH" >&2
fi

if ! CANON_ERR=$(bash scripts/check-canonical-json.sh 2>&1); then
  echo "scripts/check-canonical-json.sh failed after editing $FILE_PATH:" >&2
  echo "$CANON_ERR" >&2
  STATUS=2
fi

exit "$STATUS"
