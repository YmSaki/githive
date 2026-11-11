#!/usr/bin/env bash
# リモートへ直接 push できない環境（Claude のクラウドセッションなど）から、
# 未 push の refs/projects/* を git bundle にまとめ、base64 で標準出力に書く。
# 出力は .github/workflows/githive-relay.yml の入力 `bundle` にそのまま渡す。
# 送るものが無ければ何も出力せず終了コード 0 で終わる。
set -euo pipefail

remote="${1:-origin}"
# workflow_dispatch の 1 入力の上限（65535 文字）に余裕を持たせる。
max_chars=60000

# 削除済み ref を配送済みと誤認したり bundle の前提にしないよう、追跡 ref も削除する。
git fetch --prune -q "$remote" '+refs/projects/*:refs/githive-remote/*'

refs=()
while read -r sha ref; do
  tracking="refs/githive-remote/${ref#refs/projects/}"
  remote_sha=$(git rev-parse -q --verify "$tracking" || true)
  if [ "$sha" = "$remote_sha" ]; then
    continue
  fi
  # リモートが既に含んでいる（ローカルが遅れているだけの）ref は送らない。
  if [ -n "$remote_sha" ] && git merge-base --is-ancestor "$sha" "$remote_sha"; then
    continue
  fi
  refs+=("$ref")
done < <(git for-each-ref --format='%(objectname) %(refname)' refs/projects/)

if [ ${#refs[@]} -eq 0 ]; then
  echo "githive-relay-pack: 送る ref はありません" >&2
  exit 0
fi

bundle=$(mktemp)
trap 'rm -f "$bundle"' EXIT
exclude=()
while read -r sha; do
  exclude+=("^$sha")
done < <(git for-each-ref --format='%(objectname)' refs/githive-remote/)

git bundle create -q "$bundle" "${refs[@]}" "${exclude[@]}"
encoded=$(base64 -w0 "$bundle")

if [ ${#encoded} -gt $max_chars ]; then
  echo "githive-relay-pack: bundle が大きすぎます（${#encoded} 文字 > ${max_chars}）。ref を分けて送ってください" >&2
  exit 1
fi

echo "githive-relay-pack: ${#refs[@]} 本の ref を送ります: ${refs[*]}" >&2
printf '%s\n' "$encoded"
