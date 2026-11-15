#!/usr/bin/env bash
# scripts/check-license-boundary.py が境界違反を実際に検出することを確かめる回帰テスト。
# リポジトリの作業ツリーを一時ディレクトリへ複製し、違反を 1 つずつ入れて FAIL になることを見る。
set -euo pipefail

src="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fresh() {
  rm -rf "$tmp/repo"
  mkdir -p "$tmp/repo"
  (cd "$src" && git ls-files -z --cached --others --exclude-standard | xargs -0 -I{} cp --parents {} "$tmp/repo/" 2>/dev/null || true)
  (cd "$tmp/repo" && git init -q && git add -A)
}

expect() { # expect pass|fail <説明> [FAIL 出力に含まれるべき文字列]
  local want="$1" what="$2" needle="${3:-}" out rc=0
  out="$(cd "$tmp/repo" && python3 scripts/check-license-boundary.py 2>&1)" || rc=$?
  if [ "$want" = pass ] && [ "$rc" -ne 0 ]; then echo "FAIL ($what): expected PASS"; echo "$out"; exit 1; fi
  if [ "$want" = fail ] && [ "$rc" -eq 0 ]; then echo "FAIL ($what): expected FAIL"; echo "$out"; exit 1; fi
  if [ "$want" = fail ] && ! grep -qF -- "$needle" <<<"$out"; then echo "FAIL ($what): failed for another reason"; echo "$out"; exit 1; fi
  echo "ok: $what"
}

fresh
expect pass "unmodified tree"

fresh
mkdir -p "$tmp/repo/internal/app/leakapp"
cat > "$tmp/repo/internal/app/leakapp/leak.go" <<'GO'
package leakapp

import _ "github.com/ymsaki/githive/internal/forge/hook"
GO
expect fail "app imports internal/forge" "MIT パッケージ github.com/ymsaki/githive/internal/app/leakapp"

fresh
mkdir -p "$tmp/repo/internal/app/leakapp"
cat > "$tmp/repo/internal/app/leakapp/leak_test.go" <<'GO'
package leakapp

import _ "github.com/ymsaki/githive/internal/forge/hook"
GO
expect fail "MIT test imports internal/forge" "internal/app/leakapp が Forge"

fresh
sed -i '1d' "$tmp/repo/internal/forge/hook/prereceive.go"
expect fail "forge source without AGPL SPDX header" "prereceive.go: 先頭"

fresh
sed -i '1i // SPDX-License-'"Identifier: AGPL-3.0-or-later" "$tmp/repo/internal/app/receiveapp/receiveapp.go"
expect fail "MIT source claims AGPL" "receiveapp.go: Forge 専用ディレクトリ外"

fresh
echo "extra" >> "$tmp/repo/internal/forge/LICENSE"
expect fail "forge LICENSE differs from the AGPL text" "internal/forge/LICENSE が"

fresh
sed -i '/^github.com\/spf13\/cobra\t/d' "$tmp/repo/scripts/third-party-licenses.tsv"
expect fail "dependency missing from the manifest" "github.com/spf13/cobra@"

fresh
printf 'example.com/unused\tMIT\n' >> "$tmp/repo/scripts/third-party-licenses.tsv"
expect fail "stale manifest entry" "example.com/unused はどのバイナリ"

fresh
sed -i 's/^\(github.com\/go-git\/go-git\/v5\t\).*/\1GPL-3.0-only/' "$tmp/repo/scripts/third-party-licenses.tsv"
expect fail "CLI links a GPL dependency" "GPL-3.0-only の依存 github.com/go-git/go-git/v5"

echo "PASS: check-license-boundary.py regression"
