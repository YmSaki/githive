package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func TestCLIOversizedCommentDoesNotAdvanceRef(t *testing.T) {
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "p", "--json")
	id := createdID(t, mustRunCLI(t, dir, "issue", "new", "--title", "t", "--no-sync", "--json").stdout)
	ref := "refs/projects/issue/" + id
	before, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "comment.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", event.MaxEventBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	result := runCLI(t, dir, "issue", "comment", id, "-F", path, "--no-sync", "--json")
	if result.code != 2 || !strings.Contains(result.stdout, `"event_too_large"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", result.code, result.stdout, result.stderr)
	}
	after, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("oversized comment advanced ref")
	}
}
