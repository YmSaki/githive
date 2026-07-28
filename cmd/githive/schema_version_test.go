package main

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

// TestCLIWritesCurrentSchemaVersion checks the write side of ADR-0015:
// after raising event.SchemaVersion, the events githive actually commits
// must carry the new version rather than the "v": 1 that used to be
// hardcoded in each app service.
func TestCLIWritesCurrentSchemaVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	mustRunCLI(t, dir, "init", "--project", "versions", "--json")
	mustRunCLI(t, dir, "task", "new", "--title", "t", "--no-sync", "--json")
	mustRunCLI(t, dir, "issue", "new", "--title", "i", "--no-sync", "--json")
	mustRunCLI(t, dir, "chat", "new", "--title", "c", "-m", "hello", "--no-sync", "--json")

	refs := gitRefs(t, dir)
	if len(refs) == 0 {
		t.Fatal("no refs/projects refs were created")
	}
	want := `"v": ` + strconv.Itoa(event.SchemaVersion)
	for _, ref := range refs {
		out, err := exec.Command("git", "-C", dir, "log", "--format=%B", ref).Output()
		if err != nil {
			t.Fatalf("git log %s: %v", ref, err)
		}
		body := string(out)
		if !strings.Contains(body, want) {
			t.Errorf("ref %s: no event carries %s:\n%s", ref, want, body)
		}
		if strings.Contains(body, `"v": 1,`) {
			t.Errorf("ref %s: still writing v:1 events:\n%s", ref, body)
		}
	}
}
