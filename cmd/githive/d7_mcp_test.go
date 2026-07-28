package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These tests cover D7 (docs/13-roadmap.md「D7. CLI/MCP の露出漏れ解消
// （機能パリティ）」)'s new MCP tools, following mcp_test.go's
// newMcpTestSession/callMcpTool helpers.

// TestMcpChatEditMeta covers item 2's MCP half (chat_edit_meta already
// existed; this locks in its behavior alongside the new CLI command).
func TestMcpChatEditMeta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)

	created := callMcpTool(t, session, "chat_new", map[string]any{"title": "t1"})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("chat_new: no id in %+v", created)
	}

	callMcpTool(t, session, "chat_edit_meta", map[string]any{"id": id, "title": "t2", "status": "archived"})

	shown := callMcpTool(t, session, "chat_show", map[string]any{"id": id})
	meta, _ := shown["meta"].(map[string]any)
	if meta["title"] != "t2" || meta["status"] != "archived" {
		t.Fatalf("chat_show after edit_meta: unexpected meta %+v", meta)
	}
}

// TestMcpNotifyPostPriority covers item 3's MCP half: notify_post now takes
// a priority field, validated the same way the CLI's --priority is.
func TestMcpNotifyPostPriority(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)

	posted := callMcpTool(t, session, "notify_post", map[string]any{
		"to": []any{"all"}, "title": "hi", "priority": "low",
	})
	if posted["id"] == "" || posted["id"] == nil {
		t.Fatalf("notify_post: no id in %+v", posted)
	}

	all := callMcpTool(t, session, "notify_list", map[string]any{})
	items, _ := all["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 notification, got %+v", all)
	}
	post, _ := items[0].(map[string]any)
	if post["priority"] != "low" {
		t.Fatalf("expected priority=low, got %+v", post)
	}

	callMcpToolExpectError(t, session, "notify_post", map[string]any{
		"to": []any{"all"}, "title": "bad", "priority": "urgent",
	})
}

// TestMcpTaskNewIssueAndLink covers item 4's MCP half: task_new's issue
// field links the new task to the issue (rel=issue), and task_link
// exercises the standalone add/remove path.
func TestMcpTaskNewIssueAndLink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)

	issue := callMcpTool(t, session, "issue_new", map[string]any{"title": "bug"})
	issueID, _ := issue["id"].(string)
	if issueID == "" {
		t.Fatalf("issue_new: no id in %+v", issue)
	}

	created := callMcpTool(t, session, "task_new", map[string]any{"title": "fix it", "issue": issueID})
	taskID, _ := created["id"].(string)
	if taskID == "" {
		t.Fatalf("task_new: no id in %+v", created)
	}

	shown := callMcpTool(t, session, "task_show", map[string]any{"id": taskID})
	meta, _ := shown["meta"].(map[string]any)
	links, _ := meta["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("expected 1 link from task_new --issue, got %+v", links)
	}
	link0, _ := links[0].(map[string]any)
	if link0["rel"] != "issue" || link0["id"] != issueID {
		t.Fatalf("expected rel=issue link to %s, got %+v", issueID, link0)
	}

	callMcpTool(t, session, "task_link", map[string]any{"id": taskID, "linked_id": issueID, "rel": "chat"})
	shown = callMcpTool(t, session, "task_show", map[string]any{"id": taskID})
	meta, _ = shown["meta"].(map[string]any)
	links, _ = meta["links"].([]any)
	if len(links) != 2 {
		t.Fatalf("expected 2 links after task_link, got %+v", links)
	}

	callMcpTool(t, session, "task_link", map[string]any{"id": taskID, "linked_id": issueID, "rel": "chat", "remove": true})
	shown = callMcpTool(t, session, "task_show", map[string]any{"id": taskID})
	meta, _ = shown["meta"].(map[string]any)
	links, _ = meta["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("expected 1 link after task_link --remove, got %+v", links)
	}
}

// TestMcpWikiShowMatchesCLI covers item 6: wiki_show/wiki_log must exist
// (issue #55) and wiki_show's output must be byte-for-byte identical to the
// CLI's `githive wiki show --json` data - both call the same wikiShowJSON
// helper, so this test drives both paths against the same page and diffs
// the JSON.
func TestMcpWikiShowMatchesCLI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "wiki", "edit", "--json")
	if res.code != 0 {
		t.Fatalf("wiki edit failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var editEnv struct {
		Data struct {
			Worktree string `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &editEnv); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(editEnv.Data.Worktree, "Home.md"), []byte("# Hello\n\nこんにちは\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = runCLI(t, dir, "wiki", "save", "-m", "add Home", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("wiki save failed: code=%d stderr=%s", res.code, res.stderr)
	}

	// CLI side.
	res = runCLI(t, dir, "wiki", "show", "Home.md", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("wiki show failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var cliShown struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &cliShown); err != nil {
		t.Fatal(err)
	}

	// MCP side, against the same repo (already synced to disk by wiki save).
	session := newMcpTestSession(t, dir)
	mcpShown := callMcpTool(t, session, "wiki_show", map[string]any{"path": "Home.md"})

	if cliShown.Data["content"] != mcpShown["content"] || cliShown.Data["path"] != mcpShown["path"] {
		t.Fatalf("wiki_show MCP output does not match CLI --json data:\nCLI: %+v\nMCP: %+v", cliShown.Data, mcpShown)
	}
	if _, hasBase64 := mcpShown["content_base64"]; hasBase64 {
		t.Fatalf("expected valid-UTF-8 content to use \"content\", not \"content_base64\": %+v", mcpShown)
	}

	// Not-found must behave like the other *_show tools (an error, not a
	// silently-empty result).
	callMcpToolExpectError(t, session, "wiki_show", map[string]any{"path": "DoesNotExist.md"})

	// wiki_log matches the CLI's list shape ({"items":[...],"total":n}) and
	// finds the commit just made.
	log := callMcpTool(t, session, "wiki_log", map[string]any{})
	items, _ := log["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected at least 1 wiki log entry, got %+v", log)
	}
	if _, ok := log["total"]; !ok {
		t.Fatalf("expected a total field in wiki_log result, got %+v", log)
	}

	pathLog := callMcpTool(t, session, "wiki_log", map[string]any{"path": "Home.md"})
	pathItems, _ := pathLog["items"].([]any)
	if len(pathItems) == 0 {
		t.Fatalf("expected at least 1 wiki log entry for Home.md, got %+v", pathLog)
	}
}

// TestMcpWikiShowNonUTF8UsesBase64 covers wikiShowJSON's other branch (issue
// #53) reached through the MCP tool: invalid-UTF-8 content must come back as
// content_base64 with content absent, exactly like the CLI.
func TestMcpWikiShowNonUTF8UsesBase64(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "wiki", "edit", "--json")
	if res.code != 0 {
		t.Fatalf("wiki edit failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var editEnv struct {
		Data struct {
			Worktree string `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &editEnv); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(editEnv.Data.Worktree, "_assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	nonUTF8 := []byte{0xff, 0xfe, 0x00, 0x01}
	if err := os.WriteFile(filepath.Join(editEnv.Data.Worktree, "_assets", "bin.dat"), nonUTF8, 0o644); err != nil {
		t.Fatal(err)
	}
	res = runCLI(t, dir, "wiki", "save", "-m", "add binary asset", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("wiki save failed: code=%d stderr=%s", res.code, res.stderr)
	}

	session := newMcpTestSession(t, dir)
	mcpShown := callMcpTool(t, session, "wiki_show", map[string]any{"path": "_assets/bin.dat"})
	if _, hasContent := mcpShown["content"]; hasContent {
		t.Fatalf("expected non-UTF-8 content to omit \"content\", got %+v", mcpShown)
	}
	if mcpShown["content_base64"] == nil || mcpShown["content_base64"] == "" {
		t.Fatalf("expected non-UTF-8 content to be base64-encoded, got %+v", mcpShown)
	}
}

// TestMcpFsckAndDoctor covers item 6's fsck/doctor tools: both must exist,
// return a report with an "ok" summary field (mirroring the "verify" tool's
// documented always-succeeds-at-the-protocol-level convention), and fsck's
// compact option must be reachable.
func TestMcpFsckAndDoctor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)

	callMcpTool(t, session, "issue_new", map[string]any{"title": "t1"})

	fsckResult := callMcpTool(t, session, "fsck", map[string]any{})
	if fsckResult["ok"] != true {
		t.Fatalf("expected a clean repo to fsck ok:true, got %+v", fsckResult)
	}
	if _, ok := fsckResult["findings"]; !ok {
		t.Fatalf("expected a findings field, got %+v", fsckResult)
	}
	if _, ok := fsckResult["checkpoints"]; !ok {
		t.Fatalf("expected a checkpoints field, got %+v", fsckResult)
	}

	compact := callMcpTool(t, session, "fsck", map[string]any{"compact": true})
	if compact["ok"] != true {
		t.Fatalf("expected compact fsck to still be ok:true, got %+v", compact)
	}

	doctorResult := callMcpTool(t, session, "doctor", map[string]any{})
	if _, ok := doctorResult["checks"]; !ok {
		t.Fatalf("expected a checks field, got %+v", doctorResult)
	}
	if _, ok := doctorResult["ok"]; !ok {
		t.Fatalf("expected an ok field, got %+v", doctorResult)
	}
}
