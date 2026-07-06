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

// notifyTargetCount counts posted notifications addressed to "user:"+email
// by listing every post (unread=false) and checking targets, so it works
// regardless of which identity the test process's git config names as
// actor (unlike notify_list's unread filter, which is always "for me").
func notifyTargetCount(t *testing.T, session *mcp.ClientSession, email string) int {
	t.Helper()
	all := callMcpTool(t, session, "notify_list", map[string]any{})
	items, _ := all["items"].([]any)
	count := 0
	for _, raw := range items {
		post, _ := raw.(map[string]any)
		targets, _ := post["targets"].([]any)
		for _, target := range targets {
			if target == "user:"+email {
				count++
				break
			}
		}
	}
	return count
}

// TestMcpAutoNotifySuppressionRules exercises issue_assign's auto-notify
// hook across two distinct identities (the CLI's suppression rules -
// docs/features/notify.md「自動通知」- only make sense to test with someone
// other than the actor): a genuinely new assignee must be notified exactly
// once, re-adding the same assignee must not renotify, and assigning the
// actor to themselves must never notify at all.
func TestMcpAutoNotifySuppressionRules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // actor (git user.email) is cli@example.com
	session := newMcpTestSession(t, dir)

	callMcpTool(t, session, "users_add", map[string]any{"name": "bob", "email": "bob@example.com"})

	created := callMcpTool(t, session, "issue_new", map[string]any{"title": "t1"})
	id, _ := created["id"].(string)

	callMcpTool(t, session, "issue_assign", map[string]any{"id": id, "add": []any{"bob"}})
	if got := notifyTargetCount(t, session, "bob@example.com"); got != 1 {
		t.Fatalf("expected exactly 1 notification to a newly-assigned bob, got %d", got)
	}

	// Re-adding an already-assigned bob must not renotify.
	callMcpTool(t, session, "issue_assign", map[string]any{"id": id, "add": []any{"bob"}})
	if got := notifyTargetCount(t, session, "bob@example.com"); got != 1 {
		t.Fatalf("expected re-adding an existing assignee not to renotify, got %d notifications", got)
	}

	// Assigning the actor to themselves must never notify.
	callMcpTool(t, session, "issue_assign", map[string]any{"id": id, "add": []any{"cli@example.com"}})
	if got := notifyTargetCount(t, session, "cli@example.com"); got != 0 {
		t.Fatalf("expected self-assignment not to notify, got %d notifications", got)
	}
}

// TestMcpNotifyAckAll covers notify_ack's all=true path (list every unread
// notification addressed to the actor and acknowledge all of them), which
// TestMcpToolsExerciseCrossFeatureFlow's single-notification round trip
// does not exercise.
func TestMcpNotifyAckAll(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // actor is cli@example.com
	session := newMcpTestSession(t, dir)

	callMcpTool(t, session, "notify_post", map[string]any{"to": []any{"user:cli@example.com"}, "title": "one"})
	callMcpTool(t, session, "notify_post", map[string]any{"to": []any{"user:cli@example.com"}, "title": "two"})

	unread := callMcpTool(t, session, "notify_list", map[string]any{"unread": true})
	if items, _ := unread["items"].([]any); len(items) != 2 {
		t.Fatalf("expected 2 unread notifications before ack, got %+v", unread)
	}

	acked := callMcpTool(t, session, "notify_ack", map[string]any{"all": true})
	if ids, _ := acked["acked"].([]any); len(ids) != 2 {
		t.Fatalf("expected notify_ack all=true to acknowledge 2 ids, got %+v", acked)
	}

	unread = callMcpTool(t, session, "notify_list", map[string]any{"unread": true})
	if items, _ := unread["items"].([]any); len(items) != 0 {
		t.Fatalf("expected 0 unread notifications after ack all, got %+v", unread)
	}
}
