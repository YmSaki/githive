package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCLIAndMcpRejectInvalidWriteInputWithoutAppending(t *testing.T) {
	dir := newCLITestRepo(t)
	create := func(feature string) string {
		res := mustRunCLI(t, dir, feature, "new", "--title", "valid", "--no-sync", "--json")
		var out struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data.ID
	}
	issue, task, chat := create("issue"), create("task"), create("chat")
	refs := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname) %(objectname)").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	cases := []struct {
		name string
		cli  []string
		tool string
		args map[string]any
	}{
		{"empty issue", []string{"issue", "new", "--title", ""}, "issue_new", map[string]any{"title": ""}},
		{"empty task", []string{"task", "new", "--title", ""}, "task_new", map[string]any{"title": ""}},
		{"empty chat", []string{"chat", "new", "--title", ""}, "chat_new", map[string]any{"title": ""}},
		{"edit title", []string{"issue", "edit", issue, "--title", ""}, "issue_edit", map[string]any{"id": issue, "title": ""}},
		{"issue status", []string{"issue", "status", issue, "bogus"}, "issue_status", map[string]any{"id": issue, "to": "bogus"}},
		{"task status", []string{"task", "status", task, "bogus"}, "task_status", map[string]any{"id": task, "to": "bogus"}},
		{"chat status", []string{"chat", "edit-meta", chat, "--status", "bogus"}, "chat_edit_meta", map[string]any{"id": chat, "status": "bogus"}},
		{"chat title", []string{"chat", "edit-meta", chat, "--title", "", "--status", "open"}, "chat_edit_meta", map[string]any{"id": chat, "title": "", "status": "open"}},
		{"priority", []string{"task", "new", "--title", "valid", "--priority", "urgent"}, "task_new", map[string]any{"title": "valid", "priority": "urgent"}},
		{"due", []string{"task", "new", "--title", "valid", "--due", "tomorrow"}, "task_new", map[string]any{"title": "valid", "due": "tomorrow"}},
		{"issue link rel", []string{"issue", "link", issue, task, "--rel", "bogus"}, "issue_link", map[string]any{"id": issue, "linked_id": task, "rel": "bogus"}},
		{"task link id", []string{"task", "link", task, "short-id", "--rel", "issue"}, "task_link", map[string]any{"id": task, "linked_id": "short-id", "rel": "issue"}},
		{"comment supersedes", []string{"issue", "comment", issue, "-m", "body", "--supersedes", "x\nauthor: forged"}, "issue_comment", map[string]any{"id": issue, "message": "body", "supersedes": "x\nauthor: forged"}},
		{"chat reply", []string{"chat", "post", chat, "-m", "body", "--reply-to", "short-id"}, "chat_post", map[string]any{"id": chat, "body": "body", "reply_to": "short-id"}},
	}
	session := newMcpTestSession(t, dir)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := refs()
			args := append(append([]string{}, tc.cli...), "--no-sync", "--json")
			res := runCLI(t, dir, args...)
			if res.code != 2 || !strings.Contains(res.stdout, `"code": "invalid_input"`) {
				t.Fatalf("want invalid_input usage error: %+v", res)
			}
			if refs() != before {
				t.Fatal("invalid CLI input appended an event")
			}
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err == nil && !result.IsError {
				t.Fatalf("invalid MCP input succeeded: %+v", result)
			}
			if refs() != before {
				t.Fatal("invalid MCP input appended an event")
			}
		})
	}
	// A known enum value can still request an illegal transition. Preserve the
	// audit event and applied:false instead of treating that as malformed input.
	before := refs()
	res := mustRunCLI(t, dir, "task", "status", task, "done", "--no-sync", "--json")
	if !strings.Contains(res.stdout, `"applied": false`) || refs() == before {
		t.Fatalf("known invalid transition must be recorded: %+v", res)
	}
	callMcpTool(t, session, "chat_edit_meta", map[string]any{"id": chat, "title": "Renamed"})
	// Schema-valid empty edits retain their established no-op behavior.
	callMcpTool(t, session, "issue_edit", map[string]any{"id": issue})
	callMcpTool(t, session, "chat_edit_meta", map[string]any{"id": chat})
}
