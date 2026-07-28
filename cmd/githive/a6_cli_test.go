package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// These e2e tests cover A6 (docs/13-roadmap.md「A6. CLI 小物バグ群」): a
// handful of independent small CLI fixes each verified end-to-end against
// the real binary, the way p4_cli_test.go does for P4.

// TestCLIIssueListExcludesArchivedByDefault is item 1's CLI-level check: an
// archived issue is dropped from `issue list`'s default output (both text
// and --json), and only reappears when `--status archived` is given
// explicitly (docs/features/issue.md「archived は... 一覧の既定表示から外れ
// る」). This is a behavior change: before this fix, List() with no status
// filter returned every issue regardless of status.
func TestCLIIssueListExcludesArchivedByDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "issue", "new", "--title", "open one", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new (open) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var openCreated struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &openCreated); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}

	res = runCLI(t, dir, "issue", "new", "--title", "archived one", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new (archived) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var archivedCreated struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &archivedCreated); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	archivedID := archivedCreated.Data.ID

	for _, to := range []string{"closed", "archived"} {
		if res := runCLI(t, dir, "issue", "status", archivedID, to, "--no-sync", "--json"); res.code != 0 {
			t.Fatalf("issue status -> %s failed: code=%d stderr=%s", to, res.code, res.stderr)
		}
	}

	// Default `--json` output must not include the archived issue.
	res = runCLI(t, dir, "issue", "list", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue list --json failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if listed.Data.Total != 1 || len(listed.Data.Items) != 1 || listed.Data.Items[0]["id"] != openCreated.Data.ID {
		t.Fatalf("expected only the open issue in default --json list, got %+v", listed.Data)
	}

	// Default human-readable output must not mention the archived issue either.
	res = runCLI(t, dir, "issue", "list", "--no-sync")
	if res.code != 0 {
		t.Fatalf("issue list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if strings.Contains(res.stdout, archivedID) {
		t.Errorf("default issue list text output should not mention the archived issue:\n%s", res.stdout)
	}

	// --status archived must show it.
	res = runCLI(t, dir, "issue", "list", "--status", "archived", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue list --status archived failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var archivedListed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &archivedListed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(archivedListed.Data.Items) != 1 || archivedListed.Data.Items[0]["id"] != archivedID {
		t.Fatalf("expected the archived issue when --status archived is given, got %+v", archivedListed.Data)
	}
}
