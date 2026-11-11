#!/usr/bin/env python3
"""Run ordinary CLI/MCP acceptance against an existing, pinned executable.

Python 3.9+, Git 2.34+, OpenSSH ssh-keygen and Go (metadata only) are required.
No build/install occurs. All writable fixtures, keys and logs stay in --output.
This is smoke coverage of entrypoints, not every flag, boundary, OS or race.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import queue
import shutil
import subprocess
import sys
import threading
import time


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


class QA:
    def __init__(self, binary, output):
        self.binary, self.output = binary, output
        self.commands, self.rpc_count, self.tools = 0, 0, set()
        self.listed_tools, self.limits = set(), []
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        tmp = output / "tmp"
        tmp.mkdir()
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_TERMINAL_PROMPT="0", GOTOOLCHAIN="local", TMPDIR=str(tmp), TMP=str(tmp), TEMP=str(tmp))

    def save(self, name, value):
        (self.output / name).write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")

    def check(self, condition, message):
        if not condition:
            raise RuntimeError(message)

    def run(self, args, cwd=None, expected=0, env=None):
        args = [str(arg) for arg in args]
        try:
            p = subprocess.run(args, cwd=cwd or self.output, env=env or self.env,
                               text=True, encoding="utf-8", capture_output=True, timeout=45)
        except subprocess.TimeoutExpired as error:
            self.save("timeout.json", {"command": args, "cwd": str(cwd or self.output),
                                       "partial_stdout": repr(error.stdout), "partial_stderr": repr(error.stderr)})
            raise
        self.commands += 1
        with (self.output / "commands.jsonl").open("a", encoding="utf-8") as log:
            log.write(json.dumps(dict(index=self.commands, command=args, cwd=str(cwd or self.output),
                                     exit=p.returncode, stdout=p.stdout, stderr=p.stderr), ensure_ascii=False) + "\n")
        self.check(p.returncode == expected, f"command {self.commands}: exit {p.returncode}, expected {expected}: {args}")
        return p.stdout

    def cli(self, repo, *args, expected=0, env=None):
        result = json.loads(self.run([self.binary, "--repo", repo, "--json", "--no-sync", *args],
                                     repo, expected, env))
        self.check(result.get("ok") is (expected == 0), f"unexpected JSON success envelope: {args}")
        return result.get("data") or {}

    def repo(self, name, email):
        repo, remote = self.output / name, self.output / (name + "-remote.git")
        self.run(["git", "init", "--bare", "--initial-branch=main", remote])
        self.run(["git", "init", "--initial-branch=main", repo])
        for key, value in [("user.name", "Acceptance QA"), ("user.email", email), ("commit.gpgsign", "false")]:
            self.run(["git", "config", key, value], repo)
        self.run(["git", "commit", "--allow-empty", "-m", "seed"], repo)
        self.run(["git", "remote", "add", "origin", remote], repo)
        self.run(["git", "push", "-u", "origin", "HEAD:refs/heads/main"], repo)
        return repo, remote

    def unsigned(self):
        repo, remote = self.repo("client", "qa@example.com")
        c = lambda *args, **kwargs: self.cli(repo, *args, **kwargs)
        c("init", "--project", "acceptance"); c("init", "--project", "must-preserve")
        c("users", "add", "qa", "--email", "qa@example.com", "--role", "admin")
        c("users", "add", "bob", "--email", "bob@example.com", "--role", "member")
        c("users", "group", "set", "team", "--member", "qa", "--member", "bob")
        issue = c("issue", "new", "--title", "Ordinary issue", "--body", "Acceptance", "--label", "bug", "--assign", "qa")["id"]
        c("issue", "comment", issue, "-m", "QA comment")
        c("issue", "edit", issue, "--title", "Edited issue", "--body", "Edited body")
        c("issue", "label", issue, "--add", "reviewed", "--remove", "bug")
        c("issue", "assign", issue, "--add", "bob", "--remove", "qa")
        c("issue", "status", issue, "closed"); c("issue", "status", issue, "open")
        self.check(c("issue", "show", issue)["meta"]["title"] == "Edited issue", "issue roundtrip")
        task = c("task", "new", "--title", "QA task", "--owner", "qa", "--issue", issue)["id"]
        c("task", "note", task, "-m", "QA note"); c("task", "status", task, "doing", "-m", "Started")
        c("task", "reassign", task, "--owner", "bob")
        c("task", "link", task, issue, "--rel", "issue"); c("issue", "link", issue, task, "--rel", "task")
        c("task", "status", task, "review"); c("task", "status", task, "done", "-m", "Finished")
        self.check(c("task", "show", task)["meta"]["status"] == "done", "task roundtrip")
        chat = c("chat", "new", "--title", "QA chat", "-m", "First message")["id"]
        c("chat", "post", chat, "-m", "Second message"); c("chat", "edit-meta", chat, "--title", "Edited chat")
        c("chat", "archive", chat); c("chat", "show", chat)
        c("notify", "post", "--to", "all", "--title", "QA notice", "-m", "Please inspect")
        c("notify", "list", "--unread"); c("notify", "ack", "--all"); c("notify", "list", "--all")
        worktree = Path(c("wiki", "edit")["worktree"]).resolve()
        self.check(self.output in worktree.parents, "wiki worktree escaped fixture output")
        (worktree / "Home.md").write_text("# Acceptance\n\nSaved page.\n", encoding="utf-8")
        c("wiki", "save", "-m", "QA wiki")
        self.check(c("wiki", "show", "Home.md")["content"] == "# Acceptance\n\nSaved page.\n", "wiki roundtrip")
        c("users", "group", "remove", "team")
        for args in [("issue", "list"), ("task", "list"), ("chat", "list"), ("users", "list"),
                     ("whoami",), ("wiki", "log"), ("log",), ("status",), ("doctor",), ("fsck",)]:
            c(*args)
        c("verify", expected=4); c("fsck", "--compact"); c("sync")
        clone = self.output / "clone"
        self.run(["git", "clone", remote, clone])
        for key, value in [("user.name", "Clone QA"), ("user.email", "qa@example.com")]:
            self.run(["git", "config", key, value], clone)
        self.cli(clone, "init", "--project", "must-adopt")
        self.check(self.cli(clone, "issue", "show", issue)["meta"]["title"] == "Edited issue", "clone issue visibility")
        self.cli(clone, "wiki", "show", "Home.md")
        return repo

    def signed(self):
        repo, _ = self.repo("signed", "signed@example.com")
        key = self.output / "signing-key"
        self.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "signed@example.com", "-f", key])
        for k, v in [("gpg.format", "ssh"), ("user.signingkey", key), ("commit.gpgsign", "true")]:
            self.run(["git", "config", k, v], repo)
        c = lambda *args, **kwargs: self.cli(repo, *args, **kwargs)
        c("init", "--project", "signedqa")
        c("users", "add", "signed", "--email", "signed@example.com", "--role", "admin")
        c("users", "key", "add", "signed", "--pub", str(key) + ".pub")
        issue = c("issue", "new", "--title", "Signed issue")["id"]
        c("issue", "comment", issue, "-m", "Signed comment"); c("verify"); c("fsck")
        if os.name == "posix":
            editor = self.output / "policy-editor"
            editor.write_text("#!/bin/sh\nprintf '%s' '{\"rules\":[],\"default\":\"allow\"}' > \"$1\"\n", encoding="utf-8")
            editor.chmod(0o700)
            c("users", "policy", "edit", env={**self.env, "EDITOR": str(editor)})
            self.check(c("users", "list")["policy"]["default"] == "allow", "edited policy persisted")
        else:
            self.limits.append("CLI policy editor fixture skipped outside POSIX; shared PolicySet is exercised over MCP")
        agent = c("users", "add", "helper", "--agent", "--project", "signedqa", "--key-dir", str(self.output / "agent-keys"))
        c("whoami"); c("verify")
        for args in [("issue", "list"), ("issue", "show", issue), ("users", "list"), ("status",), ("log",), ("doctor",), ("verify",)]:
            self.run([self.binary, "--repo", repo, "--no-sync", *args], repo)
        with MCP(self, repo, "signed") as m:
            self.check(m.call("verify", {}).get("ok") is True, "signed MCP verification did not pass")
        c("users", "key", "revoke", "helper", "--pub", agent["public_key"]); c("users", "list"); c("fsck")


class MCP:
    def __init__(self, qa, repo, name):
        self.qa, self.name, self.serial = qa, name, 0
        self.log = (qa.output / (name + "-mcp.jsonl")).open("w", encoding="utf-8")
        self.stderr = (qa.output / (name + "-mcp-stderr.txt")).open("w", encoding="utf-8")
        self.process = subprocess.Popen([str(qa.binary), "--repo", str(repo), "--no-sync", "mcp", "serve"],
                                        cwd=repo, env=qa.env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=self.stderr, text=True, encoding="utf-8", bufsize=1)
        self.lines = queue.Queue()
        def read_lines():
            for line in self.process.stdout:
                self.lines.put(line)
            self.lines.put(None)
        threading.Thread(target=read_lines, daemon=True).start()

    def __enter__(self):
        try:
            self.rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "githive-acceptance", "version": "1"}})
            self.rpc("notifications/initialized", {}, notify=True)
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, exc_type, *_):
        try:
            self.process.stdin.close()
            self.process.wait(timeout=5)
        except (BrokenPipeError, subprocess.TimeoutExpired):
            self.process.kill()
            self.process.wait(timeout=5)
        finally:
            self.log.close(); self.stderr.close()
        if exc_type is None:
            self.qa.check(self.process.returncode == 0, f"MCP {self.name} shutdown exit {self.process.returncode}")

    def rpc(self, method, params, notify=False):
        self.serial += 1
        request = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notify:
            request["id"] = self.serial
        self.log.write(json.dumps({"request": request}) + "\n"); self.log.flush()
        self.process.stdin.write(json.dumps(request) + "\n"); self.process.stdin.flush()
        self.qa.rpc_count += 1
        if notify:
            return {}
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            try:
                line = self.lines.get(timeout=max(0.01, deadline - time.monotonic()))
            except queue.Empty as error:
                raise TimeoutError("MCP " + method) from error
            self.qa.check(line is not None, f"MCP closed stdout: {method}")
            response = json.loads(line)
            self.log.write(json.dumps({"response": response}) + "\n"); self.log.flush()
            if response.get("id") == request["id"]:
                self.qa.check("error" not in response, f"MCP protocol error: {response}")
                return response["result"]
        raise TimeoutError(method)

    def call(self, name, args):
        result = self.rpc("tools/call", {"name": name, "arguments": args})
        self.qa.check(not result.get("isError"), f"MCP tool failure: {name}: {result}")
        self.qa.tools.add(name)
        if "structuredContent" in result:
            return result["structuredContent"]
        for content in result.get("content", []):
            if content.get("type") == "text":
                return json.loads(content["text"])
        return {}


def mcp_acceptance(qa, repo):
    key = qa.output / "mcp-key"
    qa.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key])
    pub = Path(str(key) + ".pub").read_text(encoding="utf-8").strip()
    with MCP(qa, repo, "ordinary") as m:
        inventory = m.rpc("tools/list", {})
        qa.save("mcp-tools.json", inventory)
        qa.listed_tools = {tool["name"] for tool in inventory["tools"]}
        c = m.call
        c("users_add", {"name": "mcp-user", "email": "mcp@example.com"})
        c("users_group_set", {"name": "mcp-team", "members": ["qa", "mcp-user"]})
        c("users_key_add", {"name": "mcp-user", "pub": pub})
        issue = c("issue_new", {"title": "MCP issue", "body": "Written over stdio"})["id"]
        c("issue_comment", {"id": issue, "message": "MCP comment"})
        c("issue_edit", {"id": issue, "title": "MCP edited"})
        c("issue_label", {"id": issue, "add": ["mcp"]}); c("issue_assign", {"id": issue, "add": ["mcp-user"]})
        c("issue_status", {"id": issue, "to": "closed"}); c("issue_status", {"id": issue, "to": "open"})
        qa.check(c("issue_show", {"id": issue})["meta"]["title"] == "MCP edited", "MCP issue roundtrip")
        task = c("task_new", {"title": "MCP task", "issue": issue})["id"]
        c("task_note", {"id": task, "message": "MCP note"}); c("task_reassign", {"id": task, "owner": "mcp-user"})
        c("task_link", {"id": task, "linked_id": issue}); c("issue_link", {"id": issue, "linked_id": task})
        for status in ["doing", "review", "done"]:
            c("task_status", {"id": task, "to": status})
        qa.check(c("task_show", {"id": task})["meta"]["status"] == "done", "MCP task roundtrip")
        chat = c("chat_new", {"title": "MCP chat", "body": "First message"})["id"]
        c("chat_post", {"id": chat, "body": "Second message"}); c("chat_edit_meta", {"id": chat, "title": "Edited chat"})
        c("chat_archive", {"id": chat}); c("chat_show", {"id": chat})
        c("notify_post", {"to": ["group:mcp-team"], "title": "MCP notice", "message": "Read me"})
        c("notify_list", {"unread": True}); c("notify_ack", {"all": True})
        c("users_policy_set", {"rules": [{"refs": "refs/projects/**", "allow": ["role:admin"], "actions": ["push"]}], "default": "deny"})
        c("users_key_revoke", {"name": "mcp-user", "pub": pub}); c("users_group_remove", {"name": "mcp-team"})
        for name, args in [("issue_list", {}), ("task_list", {}), ("chat_list", {}), ("users_list", {}), ("whoami", {}),
                           ("status", {}), ("log", {}), ("wiki_show", {"path": "Home.md"}), ("wiki_log", {}), ("fsck", {}), ("doctor", {}), ("sync", {})]:
            c(name, args)
        qa.check(c("verify", {}).get("ok") is False, "unsigned verification unexpectedly passed")
        qa.save("mcp-resources.json", m.rpc("resources/list", {}))
        templates = m.rpc("resources/templates/list", {})
        qa.save("mcp-resource-templates.json", templates)
        expected = {"githive://issue/{id}", "githive://task/{id}", "githive://chat/{id}", "githive://wiki/{+path}"}
        qa.check({t["uriTemplate"] for t in templates["resourceTemplates"]} == expected, "resource template inventory changed; update harness")
        for feature, target in [("issue", issue), ("task", task), ("chat", chat), ("wiki", "Home.md")]:
            data = m.rpc("resources/read", {"uri": f"githive://{feature}/{target}"})
            qa.check(bool(data.get("contents")), f"empty {feature} resource")
            json.loads(data["contents"][0]["text"])
        qa.check(qa.cli(repo, "issue", "show", issue)["meta"]["title"] == "MCP edited", "CLI cannot read MCP issue")
    qa.check(not qa.listed_tools - qa.tools, f"uncovered MCP tools: {sorted(qa.listed_tools - qa.tools)}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path, help="absolute path to an already built executable")
    parser.add_argument("--output", required=True, type=Path, help="new or empty directory for disposable fixtures and results")
    parser.add_argument("--source", type=Path, help="optional checkout for independent source revision/status metadata")
    args = parser.parse_args()
    if not args.binary.is_absolute() or not args.binary.is_file():
        parser.error("--binary must be an existing absolute executable path")
    for program in ["git", "ssh-keygen", "go"]:
        if not shutil.which(program):
            parser.error(f"required program is missing: {program}")
    output = args.output.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=True)
    if any(output.iterdir()):
        parser.error("--output must be empty; existing results/data will not be overwritten")
    qa, failures = QA(args.binary.resolve(), output), []
    provenance = {"binary": str(qa.binary), "sha256": digest(qa.binary), "python": sys.version, "source": None}
    qa.save("provenance.json", provenance)
    try:
        provenance["go_version_m"] = qa.run(["go", "version", "-m", qa.binary])
        if args.source:
            source = args.source.resolve()
            prefix = ["git", "--no-optional-locks", "-c", f"safe.directory={source}", "-C", source]
            provenance["source"] = {"path": str(source), "commit": qa.run(prefix + ["rev-parse", "HEAD"]).strip(),
                                    "status": qa.run(prefix + ["status", "--porcelain"])}
        qa.save("provenance.json", provenance)
        repo = qa.unsigned(); qa.signed(); mcp_acceptance(qa, repo)
    except Exception as error:
        failures.append(repr(error))
    provenance["sha256_after"] = digest(qa.binary) if qa.binary.is_file() else None
    if provenance["sha256_after"] != provenance["sha256"]:
        failures.append("binary changed during acceptance")
    qa.save("provenance.json", provenance)
    summary = {"commands": qa.commands, "rpc_requests": qa.rpc_count, "tools_listed": sorted(qa.listed_tools),
               "tools_called": sorted(qa.tools), "failures": failures, "limits": qa.limits + [
                   "Ordinary smoke scenarios only; not every flag, boundary, OS, race or attack is covered.",
                   "Source metadata is independent evidence, not proof that source produced the binary.",
                   "Fixtures include disposable private keys; keep this output local."]}
    qa.save("summary.json", summary)
    print(json.dumps({"output": str(output), **summary}, ensure_ascii=False, indent=2))
    return bool(failures)


if __name__ == "__main__":
    sys.exit(main())
