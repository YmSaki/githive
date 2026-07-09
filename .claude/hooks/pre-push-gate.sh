#!/usr/bin/env bash
# PreToolUse hook (matcher: Bash).
#
# When the Bash command about to run contains "git push", require
# `go test ./...` and `python spec/validate.py` to pass first (CLAUDE.md's
# "go test ./... と python3 spec/validate.py を全 PASS で維持する" mandate).
# Any other command passes through untouched.
#
# Reads the PreToolUse hook JSON payload from stdin
# (https://code.claude.com/docs/en/hooks): {"tool_input": {"command": ...}, ...}.
# Avoids a jq dependency by parsing with python3 (already required by this
# repo for spec/validate.py), falling back to `python` if python3 is absent.
#
# PreToolUse can block: exit 2 here prevents the push from running, with the
# reason on stderr fed back to Claude.

set -uo pipefail

INPUT="$(cat)"

# JSON parsing here needs no third-party packages, so any interpreter works.
PY=python3
command -v python3 >/dev/null 2>&1 || PY=python

# spec/validate.py needs the jsonschema package specifically. On some
# machines `python3` resolves to a stub (e.g. the Windows Store alias) that
# has no packages installed, while `python` is the real, fully-provisioned
# interpreter (or vice versa on other machines/CI). Pick whichever
# interpreter can actually `import jsonschema` rather than trusting the name.
pick_spec_python() {
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1 \
      && "$candidate" -c "import jsonschema" >/dev/null 2>&1; then
      echo "$candidate"
      return 0
    fi
  done
  # Neither has jsonschema importable: fall back to python3 (CLAUDE.md's
  # canonical invocation) so the failure names the missing dependency
  # instead of masking it as "command not found".
  if command -v python3 >/dev/null 2>&1; then
    echo python3
  else
    echo python
  fi
}
SPEC_PY="$(pick_spec_python)"

COMMAND="$(printf '%s' "$INPUT" | "$PY" -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print("")
    sys.exit(0)
print(data.get("tool_input", {}).get("command") or "")
')"

case "$COMMAND" in
  *"git push"*) ;;
  *) exit 0 ;;
esac

REPO_ROOT="${CLAUDE_PROJECT_DIR:-}"
if [ -z "$REPO_ROOT" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
fi
cd "$REPO_ROOT" || exit 0

FAILURES=""

if ! GO_TEST_OUT=$(go test ./... 2>&1); then
  FAILURES="${FAILURES}--- go test ./... FAIL ---
${GO_TEST_OUT}

"
fi

if ! SPEC_OUT=$("$SPEC_PY" spec/validate.py 2>&1); then
  FAILURES="${FAILURES}--- python spec/validate.py FAIL ---
${SPEC_OUT}

"
fi

if [ -n "$FAILURES" ]; then
  printf 'git push をブロックしました（CLAUDE.md: go test / spec validate は常に全 PASS を維持する）。次のゲートが通っていません:\n\n%s' "$FAILURES" >&2
  exit 2
fi

exit 0
