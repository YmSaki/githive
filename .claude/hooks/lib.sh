#!/usr/bin/env bash
# Shared helpers for .claude/hooks/*.sh. Meant to be sourced, not executed
# directly (no shebang-driven side effects here besides function defs).

# resolve_repo_root prints the repository root: CLAUDE_PROJECT_DIR if set,
# otherwise two directories up from this file's own location
# (.claude/hooks/lib.sh -> repo root).
resolve_repo_root() {
  if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    printf '%s\n' "$CLAUDE_PROJECT_DIR"
    return 0
  fi
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  (cd "$script_dir/../.." && pwd)
}

# pick_json_python prints a python interpreter usable for parsing hook JSON
# payloads (no third-party packages required, so any interpreter works).
pick_json_python() {
  if command -v python3 >/dev/null 2>&1; then
    echo python3
  else
    echo python
  fi
}

# pick_spec_python prints a python interpreter that can actually `import
# jsonschema`, needed for spec/validate.py. On some machines python3 resolves
# to a stub (e.g. the Windows Store alias) with no packages installed, while
# python is the real interpreter (or vice versa on other machines/CI). Falls
# back to python3 (CLAUDE.md's canonical invocation) if neither has
# jsonschema, so the failure names the missing dependency instead of masking
# it as "command not found".
pick_spec_python() {
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1 \
      && "$candidate" -c "import jsonschema" >/dev/null 2>&1; then
      echo "$candidate"
      return 0
    fi
  done
  if command -v python3 >/dev/null 2>&1; then
    echo python3
  else
    echo python
  fi
}
