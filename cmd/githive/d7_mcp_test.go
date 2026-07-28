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
