package main

import (
	"os/exec"
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
