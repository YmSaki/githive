package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func unreadCount(t *testing.T, dir string) (list, status int) {
	t.Helper()
	var l struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	res := mustRunCLI(t, dir, "notify", "list", "--unread", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &l); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	var s struct {
		Data struct {
			UnreadNotify int `json:"unread_notify"`
		} `json:"data"`
	}
	res = mustRunCLI(t, dir, "status", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &s); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	return l.Data.Total, s.Data.UnreadNotify
}

// TestCLINotifyGroupTargetIsUnreadForMembers is #137: a post to
// group:<name> is unread for the group's members in notify list --unread,
// status and ack --all, and not for anyone else.
func TestCLINotifyGroupTargetIsUnreadForMembers(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t) // identity: cli@example.com
	mustRunCLI(t, dir, "users", "add", "alice", "--email", "cli@example.com", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "group", "set", "core", "--member", "alice", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "group", "set", "ops", "--member", "bob", "--no-sync", "--json")
	mustRunCLI(t, dir, "notify", "post", "--to", "group:core", "--title", "for core", "--no-sync", "--json")
	mustRunCLI(t, dir, "notify", "post", "--to", "group:ops", "--title", "for ops", "--no-sync", "--json")

	if list, status := unreadCount(t, dir); list != 1 || status != 1 {
		t.Fatalf("member: unread list=%d status=%d, want 1/1", list, status)
	}

	// bob (not in core) sees only the ops post.
	if out, err := exec.Command("git", "-C", dir, "config", "user.email", "bob@example.com").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if list, _ := unreadCount(t, dir); list != 1 {
		t.Fatalf("bob: unread list=%d, want 1 (ops only)", list)
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.email", "cli@example.com").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	mustRunCLI(t, dir, "notify", "ack", "--all", "--no-sync", "--json")
	if list, status := unreadCount(t, dir); list != 0 || status != 0 {
		t.Fatalf("after ack --all: unread list=%d status=%d, want 0/0", list, status)
	}
}
