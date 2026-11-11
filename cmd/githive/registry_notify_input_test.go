package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCLIAndMcpRejectInvalidRegistryAndNotifyWrites(t *testing.T) {
	dir := newCLITestRepo(t)
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
		{"user kind", []string{"users", "add", "alice", "--kind", "robot"}, "users_add", map[string]any{"name": "alice", "kind": "robot"}},
		{"group member", []string{"users", "group", "set", "team", "--member", "../alice"}, "users_group_set", map[string]any{"name": "team", "members": []string{"../alice"}}},
		{"target", []string{"notify", "post", "--title", "test", "--to", "alice"}, "notify_post", map[string]any{"title": "test", "to": []string{"alice"}}},
		{"group target", []string{"notify", "post", "--title", "test", "--to", "group:../team"}, "notify_post", map[string]any{"title": "test", "to": []string{"group:../team"}}},
		{"source", []string{"notify", "post", "--title", "test", "--to", "all", "--source", "issue:01j8xq4d"}, "notify_post", map[string]any{"title": "test", "to": []string{"all"}, "source": map[string]any{"kind": "issue", "id": "01j8xq4d"}}},
		{"notify title", []string{"notify", "post", "--title", "", "--to", "all"}, "notify_post", map[string]any{"title": "", "to": []string{"all"}}},
	}
	session := newMcpTestSession(t, dir)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := refs()
			args := append(append([]string{}, tc.cli...), "--no-sync", "--json")
			result := runCLI(t, dir, args...)
			if result.code != 2 || !strings.Contains(result.stdout, `"code": "invalid_input"`) {
				t.Fatalf("want invalid_input usage error: %+v", result)
			}
			if refs() != before {
				t.Fatal("invalid CLI input appended event")
			}
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err == nil && !res.IsError {
				t.Fatalf("invalid MCP input succeeded: %+v", res)
			}
			if refs() != before {
				t.Fatal("invalid MCP input appended event")
			}
		})
	}
}
