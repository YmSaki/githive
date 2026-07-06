package main

import (
	"context"
	"os/exec"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMcpTestSession wires an in-memory MCP client/server pair against dir,
// registering the exact same tools/resources `githive mcp serve` would
// (docs/15-clients.md「MCP サーバー」).
func newMcpTestSession(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{Name: "githive-test"}, nil)
	registerMcpTools(server, dir)
	registerMcpResources(server, dir)

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Wait() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clientSession.Close() })
	return clientSession
}

func callMcpTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error result: %+v", name, res.Content)
	}
	data, _ := res.StructuredContent.(map[string]any)
	return data
}

// TestMcpToolsExerciseCrossFeatureFlow drives the MCP server through
// registering a user, creating an issue by username (exercising
// usersapp.ResolveToEmail), and reading it back via both a tool and a
// resource - the same round trip an Agent connected over stdio would make
// (docs/15-clients.md「MCP サーバー」).
func TestMcpToolsExerciseCrossFeatureFlow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)

	who := callMcpTool(t, session, "whoami", map[string]any{})
	if who["registered"] != false {
		t.Fatalf("expected not yet registered, got %+v", who)
	}

	callMcpTool(t, session, "users_add", map[string]any{
		"name": "alice", "email": "cli@example.com", "roles": []string{"admin"},
	})

	who = callMcpTool(t, session, "whoami", map[string]any{})
	if who["registered"] != true || who["username"] != "alice" {
		t.Fatalf("expected registered as alice, got %+v", who)
	}

	created := callMcpTool(t, session, "issue_new", map[string]any{
		"title": "t1", "body": "b1", "assignees": []any{"alice"},
	})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("issue_new: no id in %+v", created)
	}

	shown := callMcpTool(t, session, "issue_show", map[string]any{"id": id})
	meta, _ := shown["meta"].(map[string]any)
	if meta["title"] != "t1" {
		t.Fatalf("issue_show: unexpected meta %+v", meta)
	}
	assignees, _ := meta["assignees"].([]any)
	if len(assignees) != 1 || assignees[0] != "cli@example.com" {
		t.Fatalf("expected the username 'alice' to resolve to cli@example.com, got %+v", assignees)
	}

	res, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "githive://issue/" + id})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || res.Contents[0].Text == "" {
		t.Fatalf("expected non-empty resource content, got %+v", res.Contents)
	}

	if _, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "githive://issue/does-not-exist"}); err == nil {
		t.Error("expected an error reading a nonexistent issue resource")
	}

	notifyPosted := callMcpTool(t, session, "notify_post", map[string]any{
		"to": []any{"user:alice"}, "title": "hi",
	})
	if notifyPosted["id"] == "" || notifyPosted["id"] == nil {
		t.Fatalf("notify_post: no id in %+v", notifyPosted)
	}

	unread := callMcpTool(t, session, "notify_list", map[string]any{"unread": true})
	items, _ := unread["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 unread notification, got %+v", unread)
	}

	verified := callMcpTool(t, session, "verify", map[string]any{})
	if verified["ok"] != false {
		// No signing is configured in this test repo, so every ref should
		// be flagged - if this ever passes, something stopped checking
		// signatures.
		t.Errorf("expected verify to flag unsigned commits, got %+v", verified)
	}
}
