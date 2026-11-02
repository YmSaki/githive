#!/usr/bin/env bash
# githive-relay-pack.sh が作った base64 の bundle（環境変数 GITHIVE_BUNDLE）を
# 取り込み、githive sync でリモートとマージして push する。
# .github/workflows/githive-relay.yml から呼ぶ。refs/projects/* 以外は書かない。
set -euo pipefail

remote="${1:-origin}"
githive="${GITHIVE:-githive}"
: "${GITHIVE_BUNDLE:?GITHIVE_BUNDLE が空です}"

bundle=$(mktemp)
trap 'rm -f "$bundle"' EXIT
printf '%s' "$GITHIVE_BUNDLE" | base64 -d > "$bundle"

# 送られた ref が refs/projects/ 配下だけであることを先に確かめる。
heads=$(git bundle list-heads "$bundle" | awk '{print $2}')
if [ -z "$heads" ]; then
  echo "githive-relay-apply: bundle に ref がありません" >&2
  exit 1
fi
for ref in $heads; do
  case "$ref" in
    refs/projects/*) ;;
    *) echo "githive-relay-apply: refs/projects/ 以外の ref は受け付けません: $ref" >&2; exit 1 ;;
  esac
done

# リモートの現状を追跡 ref に取り込む（bundle の前提コミットもここで揃う）。
if ! git config --get-all "remote.$remote.fetch" | grep -qxF '+refs/projects/*:refs/githive-remote/*'; then
  git config --add "remote.$remote.fetch" '+refs/projects/*:refs/githive-remote/*'
fi
git fetch -q "$remote" '+refs/projects/*:refs/githive-remote/*'

git bundle verify -q "$bundle"
# ローカルの refs/projects/ は空の前提（CI の新しい checkout）。送られた tip をそのまま置く。
git fetch -q "$bundle" '+refs/projects/*:refs/projects/*'

# イベントのスキーマを検査してから、マージと push は githive sync に任せる。
"$githive" fsck
"$githive" sync --remote "$remote"
