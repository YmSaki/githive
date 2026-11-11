package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLIAndMcpDuplicateEmailCannotAppendAnotherIdentity(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "users", "add", "alice", "--email", "shared@example.com", "--no-sync", "--json")
	head := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "rev-parse", "refs/projects/users/registry").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	before := head()
	res := runCLI(t, dir, "users", "add", "robot", "--email", "shared@example.com", "--no-sync", "--json")
	if res.code != 2 || errorCode(t, res) != "email_exists" {
		t.Fatalf("duplicate email not rejected: %+v", res)
	}
	session := newMcpTestSession(t, dir)
	callMcpToolExpectError(t, session, "users_add", map[string]any{"name": "robot", "email": "shared@example.com"})
	if got := head(); got != before {
		t.Fatalf("rejections changed registry: %s -> %s", before, got)
	}
}
