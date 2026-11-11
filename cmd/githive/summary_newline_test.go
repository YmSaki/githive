package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestCLIBlankLinesInUserInputKeepChainsReadable is issue #78's end-to-end
// regression test: titles and bodies with blank lines, CRLF, leading
// newlines and control characters end up in commit summaries, and every
// feature's chain must still read back afterwards.
func TestCLIBlankLinesInUserInputKeepChainsReadable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	const nasty = "\n\nLGTM\r\n\r\none nit\x07\n\n{\"x\":1}"

	mustRunCLI(t, dir, "init", "--project", "p"+nasty, "--json")

	issue := summaryTestID(t, mustRunCLI(t, dir, "issue", "new", "--title", "t"+nasty, "--body", nasty, "--no-sync", "--json").stdout)
	mustRunCLI(t, dir, "issue", "comment", issue, "-m", nasty, "--no-sync", "--json")
	mustRunCLI(t, dir, "issue", "comment", issue, "-m", "after", "--no-sync", "--json")
	assertCommentCount(t, mustRunCLI(t, dir, "issue", "show", issue, "--json").stdout, 2)

	task := summaryTestID(t, mustRunCLI(t, dir, "task", "new", "--title", "t"+nasty, "--body", nasty, "--no-sync", "--json").stdout)
	mustRunCLI(t, dir, "task", "note", task, "-m", nasty, "--no-sync", "--json")
	mustRunCLI(t, dir, "task", "show", task, "--json")

	chat := summaryTestID(t, mustRunCLI(t, dir, "chat", "new", "--title", "t"+nasty, "-m", nasty, "--no-sync", "--json").stdout)
	mustRunCLI(t, dir, "chat", "post", chat, "-m", nasty, "--no-sync", "--json")
	mustRunCLI(t, dir, "chat", "show", chat, "--json")

	mustRunCLI(t, dir, "notify", "post", "--title", "t"+nasty, "-m", nasty, "--to", "all", "--no-sync", "--json")
	mustRunCLI(t, dir, "notify", "post", "--title", "after", "--to", "all", "--no-sync", "--json")
	mustRunCLI(t, dir, "notify", "list", "--all", "--json")

	mustRunCLI(t, dir, "users", "add", "alice", "--email", "alice@example.com", "--display", "A"+nasty, "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "list", "--json")

	mustRunCLI(t, dir, "fsck", "--json")
}

func assertCommentCount(t *testing.T, stdout string, want int) {
	t.Helper()
	var shown struct {
		Data struct {
			Comments []any `json:"comments"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("bad json: %v\n%s", err, stdout)
	}
	if len(shown.Data.Comments) != want {
		t.Errorf("got %d comments, want %d:\n%s", len(shown.Data.Comments), want, stdout)
	}
}

func summaryTestID(t *testing.T, stdout string) string {
	t.Helper()
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("no id in response (%v):\n%s", err, stdout)
	}
	return created.Data.ID
}
