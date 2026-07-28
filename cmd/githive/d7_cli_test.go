package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// These e2e tests cover D7 (docs/13-roadmap.md「D7. CLI/MCP の露出漏れ解消
// （機能パリティ）」): CLI commands that existed in app/MCP but had no CLI
// exposure (chat edit-meta, task link), and a new validated flag (notify
// post --priority), each verified end-to-end against the real binary, the
// way p4_cli_test.go/a6_cli_test.go do for their phases.

// TestCLIChatEditMeta covers item 2: `githive chat edit-meta` did not exist
// even though chatapp.EditMeta and the MCP chat_edit_meta tool were already
// implemented. Verifies both fields update via LWW and are visible in
// `chat show --json`.
func TestCLIChatEditMeta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "chat", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("chat new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID
	if id == "" {
		t.Fatalf("chat new: no id in %s", res.stdout)
	}

	res = runCLI(t, dir, "chat", "edit-meta", id, "--title", "t2", "--status", "archived", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("chat edit-meta failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, dir, "chat", "show", id, "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("chat show failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var shown struct {
		Data struct {
			Meta map[string]any `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &shown); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if shown.Data.Meta["title"] != "t2" || shown.Data.Meta["status"] != "archived" {
		t.Errorf("unexpected meta after edit-meta: %+v", shown.Data.Meta)
	}

	// Passing neither flag must leave both fields unchanged (chatapp.EditMeta
	// treats an empty string as "leave unchanged", not "clear the field").
	res = runCLI(t, dir, "chat", "edit-meta", id, "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("chat edit-meta (no flags) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	res = runCLI(t, dir, "chat", "show", id, "--no-sync", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &shown); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if shown.Data.Meta["title"] != "t2" || shown.Data.Meta["status"] != "archived" {
		t.Errorf("edit-meta with no flags should leave title/status unchanged, got %+v", shown.Data.Meta)
	}
}
