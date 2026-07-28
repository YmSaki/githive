package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// notifyTitles lists the notification titles in a repo, via the CLI so this
// test exercises the same path a user would.
func notifyTitles(t *testing.T, dir string) []string {
	t.Helper()
	res := runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	titles := make([]string, 0, len(listed.Data.Items))
	for _, it := range listed.Data.Items {
		title, _ := it["title"].(string)
		titles = append(titles, title)
	}
	return titles
}

func newIssueForTest(t *testing.T, dir, title string) string {
	t.Helper()
	res := runCLI(t, dir, "issue", "new", "--title", title, "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	return created.Data.ID
}

// TestCLIAutoNotifyWordingComesFromNotifyapp is B1's observable goal: the
// notification an action produces is decided in one place, so the CLI and the
// MCP server cannot drift apart. It checks the CLI half here; the MCP half
// now calls the very same notifyapp helpers (a grep for the wording finds
// only internal/app/notifyapp/auto.go).
func TestCLIAutoNotifyWordingComesFromNotifyapp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	id := newIssueForTest(t, dir, "assign me")

	res := runCLI(t, dir, "issue", "assign", id, "--add", "other@example.com", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue assign failed: code=%d stderr=%s", res.code, res.stderr)
	}

	titles := notifyTitles(t, dir)
	if len(titles) != 1 {
		t.Fatalf("expected exactly 1 notification, got %v", titles)
	}
	if !strings.Contains(titles[0], "の担当になりました") || !strings.HasPrefix(titles[0], "issue "+id[:8]) {
		t.Errorf("unexpected notification title %q", titles[0])
	}
}

// TestCLIAssignSelfDoesNotNotify covers the self-suppression rule end to end.
func TestCLIAssignSelfDoesNotNotify(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	id := newIssueForTest(t, dir, "assign myself")

	// newCLITestRepo configures user.email = cli@example.com.
	res := runCLI(t, dir, "issue", "assign", id, "--add", "cli@example.com", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue assign failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if titles := notifyTitles(t, dir); len(titles) != 0 {
		t.Errorf("assigning yourself must not notify, got %v", titles)
	}
}

// TestCLIChatMentionNotifies is the new behaviour B1 adds: an @username in a
// chat body notifies that user (docs/features/chat.md「メンションと通知」).
func TestCLIChatMentionNotifies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	mustRunCLI(t, dir, "users", "add", "alice", "--email", "alice@example.com", "--kind", "human", "--no-sync", "--json")
	res := mustRunCLI(t, dir, "chat", "new", "--title", "thread", "-m", "hello", "--no-sync", "--json")
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}

	mustRunCLI(t, dir, "chat", "post", created.Data.ID, "-m", "@alice please review", "--no-sync", "--json")

	titles := notifyTitles(t, dir)
	if len(titles) != 1 || !strings.Contains(titles[0], "メンション") {
		t.Fatalf("expected one mention notification, got %v", titles)
	}
}

// TestCLIChatMentionDisabled covers githive.notify.mentions=false.
func TestCLIChatMentionDisabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	if out, err := exec.Command("git", "-C", dir, "config", "githive.notify.mentions", "false").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	mustRunCLI(t, dir, "users", "add", "alice", "--email", "alice@example.com", "--kind", "human", "--no-sync", "--json")
	res := mustRunCLI(t, dir, "chat", "new", "--title", "thread", "-m", "hello", "--no-sync", "--json")
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}

	mustRunCLI(t, dir, "chat", "post", created.Data.ID, "-m", "@alice please review", "--no-sync", "--json")
	if titles := notifyTitles(t, dir); len(titles) != 0 {
		t.Errorf("githive.notify.mentions=false must suppress mention notifications, got %v", titles)
	}
}
