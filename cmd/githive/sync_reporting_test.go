package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/core/chain"
)

// Two issue pushes have different outcomes while a divergent wiki supplies a
// warning. These are real per-ref Git outcomes, not mocked Sync results.
func newSyncReportingFixture(t *testing.T) (dir, origin, good, bad string) {
	t.Helper()
	dir = newCLITestRepo(t)
	origin = filepath.Join(t.TempDir(), "origin")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet", "--bare", origin)
	git("-C", dir, "remote", "add", "origin", origin)
	ctx := context.Background()
	for i, p := range []*string{&good, &bad} {
		id, err := issueapp.New(dir).NewIssue(ctx, []string{"accepted", "rejected"}[i], "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		*p = "refs/projects/issue/" + id
	}
	tree := git("-C", dir, "mktree")
	root := git("-C", dir, "commit-tree", tree, "-m", "wiki root")
	local := git("-C", dir, "commit-tree", tree, "-p", root, "-m", "local wiki")
	remote := git("-C", dir, "commit-tree", tree, "-p", root, "-m", "remote wiki")
	git("-C", dir, "update-ref", "refs/projects/wiki/main", remote)
	git("-C", dir, "push", "--quiet", "origin", "refs/projects/wiki/main:refs/projects/wiki/main")
	git("-C", dir, "update-ref", "refs/projects/wiki/main", local)
	if err := os.WriteFile(filepath.Join(origin, "rejected-ref"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"$(cat rejected-ref)\" ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "update"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return
}

func checkSyncPartialData(t *testing.T, data map[string]any, good, bad string) {
	t.Helper()
	items, ok := data["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("want successful issue and wiki results, got %+v", data)
	}
	seen := map[string]map[string]any{}
	for _, raw := range items {
		item := raw.(map[string]any)
		seen[item["ref"].(string)] = item
	}
	if seen[good]["action"] != "fast-forward-push" || seen[bad] != nil {
		t.Fatalf("wrong partial results: %+v", items)
	}
	wiki := seen["refs/projects/wiki/main"]
	if wiki["action"] != "wiki-diverged" || !strings.Contains(wiki["warning"].(string), "wiki edit") {
		t.Fatalf("missing wiki warning: %+v", wiki)
	}
	warnings, ok := data["warnings"].([]any)
	if !ok || len(warnings) != 1 || warnings[0].(map[string]any)["message"] != wiki["warning"] {
		t.Fatalf("missing warning list: %+v", data)
	}
	if data["total"] != float64(2) {
		t.Fatalf("partial total: %+v", data["total"])
	}
}

func TestCLISyncReportsPartialResultsAndWarningsOnFailure(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			dir, origin, good, bad := newSyncReportingFixture(t)
			args := []string{"sync"}
			if jsonMode {
				args = append(args, "--json")
			}
			res := runCLI(t, dir, args...)
			if res.code != 1 {
				t.Fatalf("want permanent failure exit 1: %+v", res)
			}
			if jsonMode {
				var payload map[string]any
				// Unmarshal rejects multiple envelopes or trailing output.
				if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
					t.Fatal(err)
				}
				failure := payload["error"].(map[string]any)
				if payload["ok"] != false || failure["code"] != "push_rejected" || failure["retryable"] != false {
					t.Fatalf("failure contract changed: %+v", payload)
				}
				checkSyncPartialData(t, failure["data"].(map[string]any), good, bad)
			} else {
				if !strings.Contains(res.stdout, "fast-forward-push\t"+good) || !strings.Contains(res.stdout, "wiki-diverged\trefs/projects/wiki/main") || !strings.Contains(res.stdout+res.stderr, "wiki edit") || !strings.Contains(res.stderr, "error:") {
					t.Fatalf("missing partial text/warning/error: %+v", res)
				}
			}
			if out, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", good).CombinedOutput(); err != nil {
				t.Fatalf("successful issue was not delivered: %v %s", err, out)
			}
		})
	}
}

func TestMcpSyncReportsPartialResultsAndWarningsOnFailure(t *testing.T) {
	dir, _, good, bad := newSyncReportingFixture(t)
	session := newMcpTestSession(t, dir)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "sync", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("failed sync must remain a tool error: %+v", result)
	}
	data, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("missing structured partial output: %+v", result)
	}
	checkSyncPartialData(t, data, good, bad)
	failure := data["error"].(map[string]any)
	if failure["code"] != "push_rejected" || failure["retryable"] != false {
		t.Fatalf("wrong failure: %+v", failure)
	}
	// Clients without structured-output support still receive the same results.
	if len(result.Content) != 1 {
		t.Fatalf("missing text fallback: %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, good) || !strings.Contains(text.Text, "wiki-diverged") {
		t.Fatalf("missing partial text: %+v", result.Content)
	}
}

func TestMcpSyncWikiWarningMatchesCLIJSON(t *testing.T) {
	dir, _, _, _ := newSyncReportingFixture(t)
	// Restrict to wiki so this run succeeds with a divergence warning.
	cli := runCLI(t, dir, "sync", "--kind", "wiki", "--json")
	if cli.code != 0 {
		t.Fatalf("wiki warning should succeed: %+v", cli)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(cli.stdout), &payload); err != nil {
		t.Fatal(err)
	}
	data := callMcpTool(t, newMcpTestSession(t, dir), "sync", map[string]any{"kinds": []string{"wiki"}})
	if !reflect.DeepEqual(payload["data"].(map[string]any)["items"], data["items"]) || !reflect.DeepEqual(payload["warnings"], data["warnings"]) {
		t.Fatalf("CLI/MCP warning mismatch: cli=%+v mcp=%+v", payload, data)
	}
	if len(data["warnings"].([]any)) != 1 {
		t.Fatalf("missing MCP warning: %+v", data)
	}
}

func TestSyncPartialFailurePreservesErrorClassification(t *testing.T) {
	causes := []error{
		&chain.ShallowHistoryError{},
		syncapp.ErrRetriesExhausted,
		syncapp.ErrPushRejected,
		&syncapp.TrustRootConflictError{Ref: "refs/projects/users/registry", LocalRoots: []string{"local"}, RemoteRoots: []string{"remote"}},
	}
	for _, cause := range causes {
		wantCode, want := classifyError(cause)
		gotCode, got := classifyError(&syncFailedError{cause: cause})
		if gotCode != wantCode || got.Code != want.Code || got.Message != want.Message || got.Retryable != want.Retryable {
			t.Fatalf("classification changed: got %+v want %+v", got, want)
		}
		for key, value := range want.Data {
			if !reflect.DeepEqual(got.Data[key], value) {
				t.Fatalf("lost diagnostic %s: %+v", key, got.Data)
			}
		}
		items, ok := got.Data["items"].([]any)
		if !ok || len(items) != 0 || got.Data["total"] != 0 {
			t.Fatalf("empty partial result must be a list: %+v", got.Data)
		}
	}
}
