#!/usr/bin/env bash
# A deleted remote ref must be sent again, without stale bundle prerequisites.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git init --bare -q "$tmp/remote.git"
git init -q "$tmp/client"
cd "$tmp/client"
git config user.name relay-test
git config user.email relay-test@example.com
git remote add origin "$tmp/remote.git"
git -c commit.gpgSign=false commit --allow-empty -qm initial
ref=refs/projects/meta/config
git update-ref "$ref" HEAD

# First delivery is self-contained; an already delivered tip needs no bundle.
bash "$script_dir/githive-relay-pack.sh" > "$tmp/first.b64"
test -s "$tmp/first.b64"
git push -q origin "$ref"
bash "$script_dir/githive-relay-pack.sh" > "$tmp/unchanged.b64"
test ! -s "$tmp/unchanged.b64"

# Simulate a remote migration/deletion while the client retains its local tip.
git --git-dir="$tmp/remote.git" update-ref -d "$ref"
bash "$script_dir/githive-relay-pack.sh" > "$tmp/recovery.b64"
test -s "$tmp/recovery.b64"
if git show-ref --verify --quiet refs/githive-remote/meta/config; then
  echo "stale tracking ref survived fetch" >&2
  exit 1
fi

# A fresh receiver must be able to import the recovery bundle without objects
# from the deleted remote history.
base64 -d "$tmp/recovery.b64" > "$tmp/recovery.bundle"
git init -q "$tmp/receiver"
git -C "$tmp/receiver" bundle verify "$tmp/recovery.bundle"
git -C "$tmp/receiver" fetch -q "$tmp/recovery.bundle" "$ref:$ref"
test "$(git rev-parse "$ref")" = "$(git -C "$tmp/receiver" rev-parse "$ref")"
echo "relay pack regression: PASS"
