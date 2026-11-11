package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCLINotifyAckAcceptsDisplayedID(t *testing.T) {
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "notify", "post", "--title", "read me", "--to", "all", "--no-sync", "--json")
	listed := mustRunCLI(t, dir, "notify", "list", "--unread")
	fields := strings.Fields(listed.stdout)
	if len(fields) == 0 || len(fields[0]) != 8 {
		t.Fatalf("expected displayed short ID: %s", listed.stdout)
	}
	acked := mustRunCLI(t, dir, "notify", "ack", fields[0], "--no-sync", "--json")
	var out struct {
		Data struct {
			Acked []string `json:"acked"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(acked.stdout), &out); err != nil || len(out.Data.Acked) != 1 || len(out.Data.Acked[0]) != 26 {
		t.Fatalf("ack output must contain full ID: %s (%v)", acked.stdout, err)
	}
	unread := mustRunCLI(t, dir, "notify", "list", "--unread", "--json")
	var list struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(unread.stdout), &list); err != nil || list.Data.Total != 0 {
		t.Fatalf("short ack left notification unread: %s (%v)", unread.stdout, err)
	}
	head := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "rev-parse", "refs/projects/notify/stream").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := head()
	bad := runCLI(t, dir, "notify", "ack", "01j8xq4f3nbz9k7w2m5e8h1t64", "--no-sync", "--json")
	if bad.code == 0 || !strings.Contains(bad.stdout, `"code": "not_found"`) {
		t.Fatalf("unknown notification should be not_found: %+v", bad)
	}
	if head() != before {
		t.Fatal("unknown ID appended ack")
	}
}

func TestMcpNotifyAckResolvesShortIDs(t *testing.T) {
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	post := callMcpTool(t, session, "notify_post", map[string]any{"to": []string{"all"}, "title": "read me"})
	id := post["id"].(string)
	ack := callMcpTool(t, session, "notify_ack", map[string]any{"ids": []string{strings.ToUpper(id[:8])}})
	ids := ack["acked"].([]any)
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("ack did not return full ID: %v", ack)
	}
	unread := callMcpTool(t, session, "notify_list", map[string]any{"unread": true})
	if len(unread["items"].([]any)) != 0 {
		t.Fatalf("short ack left unread posts: %v", unread)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "notify_ack", Arguments: map[string]any{"ids": []string{"missing-notification"}}})
	if err == nil && !result.IsError {
		t.Fatal("unknown MCP notification succeeded")
	}
}
