#!/usr/bin/env python3
"""Install this checkout into a disposable GOBIN and check its public entrypoints.

Unlike acceptance.py, this deliberately builds the selected source checkout.
It does not install globally, create repository data or contact a Git remote.
Go may download declared module dependencies through its normal module proxy.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--output", type=Path, required=True, help="new or empty disposable directory")
    args = parser.parse_args()
    source, output = args.source.resolve(), args.output.resolve()
    if not (source / "go.mod").is_file():
        parser.error("--source must contain the githive checkout's go.mod")
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    if any(output.iterdir()):
        parser.error("--output must be empty; existing files will not be overwritten")
    install_bin = output / "bin"
    install_bin.mkdir()
    env = {**os.environ, "GOBIN": str(install_bin), "GOTOOLCHAIN": "local", "GIT_OPTIONAL_LOCKS": "0"}
    report = {"source": str(source), "gobin": str(install_bin), "commands": [], "failures": []}

    def save():
        (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")

    def run(*args):
        command = [str(arg) for arg in args]
        p = subprocess.run(command, cwd=source, env=env, text=True, encoding="utf-8",
                           capture_output=True, timeout=180)
        report["commands"].append({"command": command, "exit": p.returncode, "stdout": p.stdout, "stderr": p.stderr})
        save()
        if p.returncode:
            raise RuntimeError(f"command failed: {command}")
        return p.stdout

    try:
        version = run("git", "--version")
        parsed = re.search(r"git version (\d+)\.(\d+)\.(\d+)", version)
        if not parsed or tuple(map(int, parsed.groups())) < (2, 34, 0):
            raise RuntimeError("Git 2.34 or newer is required")
        run("go", "version")
        run("go", "install", "./cmd/githive")
        binary = install_bin / ("githive.exe" if os.name == "nt" else "githive")
        for command in [(binary, "--help"), (binary, "issue", "--help"), (binary, "mcp", "serve", "--help")]:
            run(*command)
        metadata = run("go", "version", "-m", binary)
        if "github.com/ymsaki/githive/cmd/githive" not in metadata:
            raise RuntimeError("installed binary has unexpected Go package metadata")
    except Exception as error:
        report["failures"].append(repr(error))
    save()
    print(json.dumps({"report": str(output / "report.json"), "failures": report["failures"]}, indent=2))
    return bool(report["failures"])


if __name__ == "__main__":
    sys.exit(main())
