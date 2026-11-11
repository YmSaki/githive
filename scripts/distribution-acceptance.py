#!/usr/bin/env python3
"""Verify exact public-module installation and lifecycle in disposable scratch.

Run after publishing the reviewed commit to the source repository. Requires
Python 3.9+, Go, Git, ssh-keygen and a POSIX sh. Only publish/ is upload-safe;
private/ holds raw logs, fixture repositories and disposable private keys.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

MODULE = "github.com/ymsaki/githive"
OLD_COMMIT = "ff1ab6707ff9fea8fda3aea200f726f89012c798"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-commit", required=True, help="exact 40-character published commit")
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--output", type=Path, required=True, help="new or empty disposable directory")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
        parser.error("--source-commit must be a full lowercase Git SHA")
    source, output = args.source.resolve(), args.output.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    if any(output.iterdir()):
        parser.error("--output must be empty; existing data will not be overwritten")
    private, publish = output / "private", output / "publish"
    private.mkdir(mode=0o700); publish.mkdir(mode=0o700)
    bin_dir = private / "bin"
    bin_dir.mkdir()
    binary = bin_dir / ("githive.exe" if os.name == "nt" else "githive")
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0",
               GIT_OPTIONAL_LOCKS="0", GOTOOLCHAIN="local", GOBIN=str(bin_dir),
               GOPATH=str(private / "go-path"), GOCACHE=str(private / "go-cache"),
               GOMODCACHE=str(private / "go-mod"))
    records, stage = [], "checkout provenance"
    provenance = {"source_commit": args.source_commit, "module": MODULE,
                  "old_commit": OLD_COMMIT, "platform": platform.system() + "/" + platform.machine()}
    summary = {"source_commit": args.source_commit, "checks": {}, "failures": [], "complete": False,
               "limits": ["Synthetic local fixtures only; hosted transport and every flag are not covered.",
                          "Only publish/ is intended for artifact upload; private/ contains disposable keys and raw logs."]}

    def save(path, value):
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")

    def run(command, cwd=source):
        command = [str(x) for x in command]
        p = subprocess.run(command, cwd=cwd, env=env, text=True, encoding="utf-8",
                           capture_output=True, timeout=600)
        records.append({"command": command, "cwd": str(cwd), "exit": p.returncode,
                        "stdout": p.stdout, "stderr": p.stderr})
        save(private / "commands.json", records)
        if p.returncode:
            raise RuntimeError(f"command {len(records)} failed")
        return p.stdout

    def check(condition, message):
        if not condition:
            raise RuntimeError(message)

    def cli(repo, *command):
        result = json.loads(run([binary, "--repo", repo, "--json", "--no-sync", *command], repo))
        check(result.get("ok") is True, "CLI success envelope missing")
        return result.get("data") or {}

    def install(commit):
        resolved = json.loads(run(["go", "list", "-m", "-json", MODULE + "@" + commit]))
        origin_hash = resolved.get("Origin", {}).get("Hash")
        check(not origin_hash or origin_hash == commit, "module origin differs from requested commit")
        run(["go", "install", MODULE + "/cmd/githive@" + commit])
        metadata = run(["go", "version", "-m", binary])
        lines = [line.split() for line in metadata.splitlines()]
        check(["path", MODULE + "/cmd/githive"] in lines, "installed package path differs")
        check(any(len(line) >= 3 and line[:3] == ["mod", MODULE, resolved["Version"]] for line in lines),
              "installed module version differs from resolved exact commit")
        return resolved["Version"], metadata, origin_hash

    def snapshot(repo, issue):
        return (run(["git", "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/"], repo),
                (repo / ".git" / "config").read_bytes(), cli(repo, "issue", "show", issue))

    try:
        head = run(["git", "rev-parse", "HEAD"]).strip()
        check(head == args.source_commit, "checkout HEAD differs from requested published commit")
        check(not run(["git", "status", "--porcelain"]).strip(), "checkout is not clean")
        provenance["checkout_commit"] = head
        provenance["go_version"] = run(["go", "version"]).strip()
        provenance["git_version"] = run(["git", "--version"]).strip()
        stage = "old module install and fresh lifecycle fixture"
        old_version, _, _ = install(OLD_COMMIT)
        provenance["old_version"] = old_version
        provenance["old_binary_sha256"] = digest(binary)
        repo = private / "lifecycle-repo"
        run(["git", "init", "--quiet", "--initial-branch=main", repo])
        for key, value in [("user.name", "Lifecycle QA"), ("user.email", "lifecycle@example.invalid"),
                           ("commit.gpgsign", "false")]:
            run(["git", "config", key, value], repo)
        cli(repo, "init", "--project", "lifecycle-qa")
        issue = cli(repo, "issue", "new", "--title", "Synthetic old-version lifecycle fixture")["id"]
        before = snapshot(repo, issue)
        stage = "exact candidate update"
        version, metadata, origin_hash = install(args.source_commit)
        candidate_hash = digest(binary)
        provenance.update(candidate_version=version, candidate_origin_hash=origin_hash,
                          binary_sha256=candidate_hash, go_version_m=metadata.replace(str(private), "<private-scratch>"))
        check(snapshot(repo, issue) == before, "update changed refs, config or issue")
        summary["checks"]["old_to_candidate_preserves_refs_config_issue"] = True
        stage = "binary-only uninstall and exact reinstall"
        binary.unlink()
        check(not binary.exists(), "binary-only uninstall did not remove executable")
        check(run(["git", "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/"], repo) == before[0]
              and (repo / ".git" / "config").read_bytes() == before[1], "uninstall changed repository data")
        summary["checks"]["binary_only_uninstall_preserves_refs_config"] = True
        install(args.source_commit)
        check(digest(binary) == candidate_hash, "reinstall produced a different candidate binary")
        check(snapshot(repo, issue) == before, "reinstall changed refs, config or issue")
        summary["checks"]["reinstall_preserves_data_and_binary_digest"] = True
        stage = "full CLI and MCP acceptance"
        acceptance = private / "acceptance"
        run([sys.executable, source / "scripts" / "acceptance.py", "--binary", binary,
             "--output", acceptance, "--source", source])
        accepted = json.loads((acceptance / "summary.json").read_text())
        accepted_provenance = json.loads((acceptance / "provenance.json").read_text())
        check(not accepted["failures"], "acceptance reported failures")
        check(set(accepted["tools_listed"]) == set(accepted["tools_called"]), "MCP entrypoints remain uncovered")
        check(accepted_provenance["sha256"] == candidate_hash, "acceptance tested a different binary")
        check(accepted_provenance["sha256_after"] == candidate_hash, "acceptance binary changed during testing")
        summary["acceptance"] = {k: accepted[k] for k in ["commands", "rpc_requests", "tools_listed", "tools_called", "limits"]}
        stage = "signed MCP mutations and literal shell snippet"
        spec = importlib.util.spec_from_file_location("acceptance", source / "scripts" / "acceptance.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        extras = private / "extras"
        extras.mkdir(mode=0o700)
        qa = module.QA(binary, extras)
        qa.env.update(env, TMPDIR=str(extras / "tmp"), TMP=str(extras / "tmp"), TEMP=str(extras / "tmp"))
        with module.MCP(qa, acceptance / "signed", "signed-mutations") as m:
            signed_issue = m.call("issue_new", {"title": "Signed MCP distribution verification"})["id"]
            m.call("issue_comment", {"id": signed_issue, "message": "Signed MCP comment"})
            shown = m.call("issue_show", {"id": signed_issue})
            check(shown["meta"]["title"] == "Signed MCP distribution verification", "signed MCP readback failed")
            check("Signed MCP comment" in json.dumps(shown.get("comments", [])), "signed MCP comment was not retained")
            check(m.call("verify", {}).get("ok") is True, "signed MCP verification failed")
        summary["checks"]["signed_mcp_create_comment_read_verify"] = True
        snippet_repo = extras / "snippet-repo"
        qa.run(["git", "init", "--quiet", snippet_repo])
        for key, value in [("user.name", "Snippet QA"), ("user.email", "snippet@example.invalid"), ("commit.gpgsign", "false")]:
            qa.run(["git", "config", key, value], snippet_repo)
        qa.cli(snippet_repo, "init", "--project", "snippetqa")
        email = "qa$(>snippet-expanded)@example.com"
        agent = qa.cli(snippet_repo, "users", "add", "snippet-agent", "--agent", "--email", email,
                       "--project", "snippetqa", "--key-dir", extras / "keys with ' and $ and `")
        qa.run(["sh", "-c", agent["config_snippet"]], snippet_repo)
        check(not (snippet_repo / "snippet-expanded").exists(), "snippet performed shell substitution")
        check(qa.run(["git", "config", "user.email"], snippet_repo).strip() == email, "snippet changed literal email")
        check(qa.run(["git", "config", "user.signingkey"], snippet_repo).strip() == agent["private_key"], "snippet changed key path")
        summary["checks"]["snippet_blocks_substitution_and_preserves_literals"] = True
        check(digest(binary) == candidate_hash, "candidate binary changed during QA")
        stage = "publish safe artifacts"
        candidate_copy = publish / "candidate.tmp"
        shutil.copyfile(binary, candidate_copy)
        check(digest(candidate_copy) == candidate_hash, "published copy differs from tested binary")
        (publish / "bin").mkdir()
        public_binary = publish / "bin" / binary.name
        candidate_copy.rename(public_binary)
        public_binary.chmod(0o755)
        summary.update(binary_sha256=candidate_hash, candidate_version=version, complete=True)
    except Exception as error:
        save(private / "failure.json", {"stage": stage, "error": repr(error)})
        summary["failures"].append({"stage": stage, "type": type(error).__name__, "message": "Stage failed; raw fixture logs were not published."})
    finally:
        summary["driver_commands"] = len(records)
        save(publish / "summary.json", summary)
        save(publish / "provenance.json", provenance)
        print(json.dumps(summary, ensure_ascii=False, indent=2))
    return bool(summary["failures"])


if __name__ == "__main__":
    sys.exit(main())
