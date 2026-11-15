#!/usr/bin/env python3
"""MIT / AGPL のライセンス境界と、Forge の依存方向を検査する（ADR-0010、ADR-0019）。

検査すること:

1. MIT 側のパッケージ（Forge 専用ディレクトリ以外のすべて。cmd/githive、internal/core、
   internal/app を含む）が、Forge 専用パッケージを直接・推移的に import しない（テストの import を含む）。
2. Forge 専用ディレクトリ（cmd/githive-forge、internal/forge、web）が LICENSE（AGPL-3.0 全文）を持ち、
   ソースファイルが先頭付近に SPDX-License-Identifier: AGPL-3.0-or-later を持つ。
   Forge 専用ディレクトリ以外のソースは AGPL の SPDX 表示を持たない。
3. 両バイナリにリンクされる第三者モジュールが scripts/third-party-licenses.tsv に過不足なく記載され、
   ライセンスファイルを持つ。CLI（cmd/githive）の依存に GPL 系のライセンスが無い。

--notices PATH を付けると、githive-forge の結合配布物に同梱する第三者ライセンス表示を PATH に書き出す。
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
MODULE = "github.com/ymsaki/githive"
FORGE_DIRS = ["cmd/githive-forge", "internal/forge", "web"]
BINARIES = {"cli": "./cmd/githive", "forge": "./cmd/githive-forge"}
MANIFEST = os.path.join("scripts", "third-party-licenses.tsv")
# https://www.gnu.org/licenses/agpl-3.0.txt の SHA-256。
AGPL_SHA256 = "0d96a4ff68ad6d4b6f1f30f713b18d5184912ba8dd389f86aa7710db079abcb0"
# 自分自身にマッチしないよう連結で組み立てる。
AGPL_SPDX = "SPDX-License-" + "Identifier: AGPL-3.0-or-later"
SOURCE_EXTS = {".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".css", ".scss", ".html"}
SPDX_HEAD_LINES = 5
# CLI は MIT として配るので、強いコピーレフトの依存を持たない（ADR-0010）。
CLI_FORBIDDEN = re.compile(r"\b(AGPL|GPL|LGPL|SSPL|EUPL|OSL)-")
LICENSE_FILE = re.compile(r"^(LICEN[CS]E|COPYING)(\.(md|txt))?$", re.IGNORECASE)

errors = []


def fail(msg):
    errors.append(msg)


def go(*args):
    return subprocess.run(["go", *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def is_forge_pkg(import_path):
    for d in FORGE_DIRS:
        p = MODULE + "/" + d
        if import_path == p or import_path.startswith(p + "/"):
            return True
    return False


def is_forge_path(rel):
    rel = rel.replace(os.sep, "/")
    return any(rel == d or rel.startswith(d + "/") for d in FORGE_DIRS)


def go_list_packages():
    out = go("list", "-json", "./...")
    dec = json.JSONDecoder()
    i, pkgs = 0, []
    while i < len(out):
        while i < len(out) and out[i].isspace():
            i += 1
        if i >= len(out):
            break
        obj, i = dec.raw_decode(out, i)
        pkgs.append(obj)
    return pkgs


def check_import_direction():
    pkgs = go_list_packages()
    for p in pkgs:
        ip = p["ImportPath"]
        if is_forge_pkg(ip):
            continue
        reach = set(p.get("Deps", [])) | set(p.get("TestImports", [])) | set(p.get("XTestImports", []))
        bad = sorted(d for d in reach if is_forge_pkg(d))
        if bad:
            fail(f"MIT パッケージ {ip} が Forge 専用パッケージに依存している: {', '.join(bad)}")
    # テストが import した MIT パッケージから先の推移依存は、そのパッケージ自身の Deps の検査で捕まる。


def tracked_files():
    out = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True).stdout
    files = [f for f in out.decode().split("\0") if f]
    # まだコミットしていない新規ファイルも検査対象にする（ローカル実行時）。
    others = subprocess.run(["git", "ls-files", "-z", "--others", "--exclude-standard"], cwd=ROOT,
                            check=True, capture_output=True).stdout
    files += [f for f in others.decode().split("\0") if f]
    return sorted(set(f for f in files if os.path.isfile(os.path.join(ROOT, f))))


def head(path, n):
    with open(os.path.join(ROOT, path), encoding="utf-8", errors="replace") as fh:
        return [next(fh, "") for _ in range(n)]


def check_license_notices():
    for d in FORGE_DIRS:
        if not os.path.isdir(os.path.join(ROOT, d)):
            continue
        lic = os.path.join(ROOT, d, "LICENSE")
        if not os.path.isfile(lic):
            fail(f"{d}/LICENSE が無い（AGPL-3.0 の全文を置く）")
            continue
        with open(lic, "rb") as fh:
            if hashlib.sha256(fh.read()).hexdigest() != AGPL_SHA256:
                fail(f"{d}/LICENSE が AGPL-3.0 の全文（FSF 配布版）と一致しない")
    for f in tracked_files():
        ext = os.path.splitext(f)[1]
        if ext not in SOURCE_EXTS:
            continue
        has_agpl = any(AGPL_SPDX in line for line in head(f, SPDX_HEAD_LINES))
        if is_forge_path(f):
            if not has_agpl:
                fail(f"{f}: 先頭 {SPDX_HEAD_LINES} 行に '{AGPL_SPDX}' が無い")
        elif has_agpl:
            fail(f"{f}: Forge 専用ディレクトリ外のファイルが AGPL を表示している（MIT 側に置くか、Forge 専用ディレクトリへ移す）")


def read_manifest():
    entries = {}
    with open(os.path.join(ROOT, MANIFEST), encoding="utf-8") as fh:
        for n, line in enumerate(fh, 1):
            line = line.rstrip("\n")
            if not line or line.startswith("#"):
                continue
            parts = line.split("\t")
            if len(parts) != 2 or not parts[0] or not parts[1]:
                fail(f"{MANIFEST}:{n}: '<module>\\t<SPDX>' の形式でない")
                continue
            if parts[0] in entries:
                fail(f"{MANIFEST}:{n}: {parts[0]} が重複している")
            entries[parts[0]] = parts[1]
    return entries


def binary_modules(pkg):
    out = go("list", "-deps", "-f",
             "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}", pkg)
    mods = {}
    for line in out.splitlines():
        if line:
            path, version, d = line.split("\t")
            mods[path] = (version, d)
    return mods


def license_files(d):
    if not d or not os.path.isdir(d):
        return []
    return sorted(f for f in os.listdir(d) if LICENSE_FILE.match(f))


def check_third_party():
    manifest = read_manifest()
    mods = {name: binary_modules(pkg) for name, pkg in BINARIES.items()}
    used = set()
    for name, deps in mods.items():
        for path, (version, d) in sorted(deps.items()):
            used.add(path)
            spdx = manifest.get(path)
            if spdx is None:
                fail(f"{BINARIES[name]} の依存 {path}@{version} が {MANIFEST} に無い（ライセンスを確認して追記する）")
                continue
            if not license_files(d):
                fail(f"{path}@{version} にライセンスファイルが見つからない（{d}）")
            if name == "cli" and CLI_FORBIDDEN.search(spdx):
                fail(f"MIT の CLI が {spdx} の依存 {path} をリンクしている（ADR-0010）")
    for path in sorted(set(manifest) - used):
        fail(f"{MANIFEST} の {path} はどのバイナリの依存でもない（削除する）")
    return manifest, mods


def write_notices(out_path, manifest, mods):
    lines = [
        "githive-forge third-party notices",
        "",
        "githive-forge is licensed AGPL-3.0-or-later. It links the MIT-licensed githive",
        "shared code (internal/core, internal/app; see LICENSE at the repository root) and the",
        "third-party Go modules listed below. The corresponding source of each module is the",
        "module version recorded here, verified by go.sum.",
        "",
        "=" * 78,
        f"{MODULE} (shared code, MIT)",
        "=" * 78,
    ]
    with open(os.path.join(ROOT, "LICENSE"), encoding="utf-8") as fh:
        lines.append(fh.read().rstrip("\n"))
    lines.append("")
    for path, (version, d) in sorted(mods["forge"].items()):
        lines.append("=" * 78)
        lines.append(f"{path} {version}  ({manifest.get(path, 'UNKNOWN')})")
        lines.append("=" * 78)
        for f in license_files(d):
            with open(os.path.join(d, f), encoding="utf-8", errors="replace") as fh:
                lines.append(fh.read().rstrip("\n"))
            lines.append("")
    with open(out_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(lines) + "\n")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--notices", help="githive-forge の第三者ライセンス表示を書き出す先")
    args = ap.parse_args()

    check_import_direction()
    check_license_notices()
    manifest, mods = check_third_party()

    if errors:
        print("FAIL: license boundary check")
        for e in errors:
            print("  " + e)
        return 1
    if args.notices:
        write_notices(args.notices, manifest, mods)
        print(f"wrote {args.notices}")
    print("PASS: MIT/AGPL boundary, forge import direction, third-party licenses")
    return 0


if __name__ == "__main__":
    sys.exit(main())
