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

// TestCLINotifyPostPriority covers item 3: notify post now exposes
// --priority, validated against low/normal/high, with an invalid value
// exiting 2 (usage error, docs/10-cli-spec.md「終了コード」).
func TestCLINotifyPostPriority(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "notify", "post", "--to", "all", "--title", "hi", "--priority", "high", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify post --priority high failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
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
	if len(listed.Data.Items) != 1 || listed.Data.Items[0]["priority"] != "high" {
		t.Fatalf("expected exactly 1 notification with priority=high, got %+v", listed.Data.Items)
	}

	res = runCLI(t, dir, "notify", "post", "--to", "all", "--title", "bad", "--priority", "urgent", "--no-sync", "--json")
	if res.code != 2 {
		t.Fatalf("expected exit code 2 for an invalid --priority, got code=%d stdout=%s", res.code, res.stdout)
	}
	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &failed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if failed.OK || failed.Error.Code != "invalid_priority" {
		t.Errorf("unexpected failure envelope for invalid priority: %+v", failed)
	}

	// Omitting --priority entirely must still work (priority stays unset).
	res = runCLI(t, dir, "notify", "post", "--to", "all", "--title", "no prio", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify post without --priority should still succeed: code=%d stderr=%s", res.code, res.stderr)
	}
}
