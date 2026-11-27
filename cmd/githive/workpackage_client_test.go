package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ymsaki/githive/internal/app/fsckapp"
)

func requireGitBin(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func wpLogKinds(items []any) map[string]bool {
	kinds := map[string]bool{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["feature"] == "workpackage" {
			kinds[m["kind"].(string)] = true
		}
	}
	return kinds
}

// `githive log` (CLI and MCP) shows workpackage lifecycle events and still
// hides the workpackage.checkpoint fsck --compact appends.
func TestCLIAndMCPLogIncludeWorkpackage(t *testing.T) {
	requireGitBin(t)
	dir := newCLITestRepo(t)
	id := wpRun(t, dir, 0, "wp", "new", "--title", "WP").Data["id"].(string)
	wpRun(t, dir, 0, "wp", "start", id)
	wpRun(t, dir, 0, "wp", "submit", id, "--no-commit")
	gitIn(t, dir, "config", "user.email", "rev@example.com")
	wpRun(t, dir, 0, "wp", "accept", id)
	report, err := fsckapp.New(dir).Run(context.Background(), fsckapp.Options{Compact: true, MaxEvents: 2})
	if err != nil || len(report.Checkpoints) == 0 {
		t.Fatalf("fixture checkpoint: %v %+v", err, report)
	}

	res := runCLI(t, dir, "log", "--no-sync", "--json")
	var out struct {
		Data struct {
			Items []any `json:"items"`
		} `json:"data"`
	}
	if res.code != 0 || json.Unmarshal([]byte(res.stdout), &out) != nil {
		t.Fatalf("log: %d %s %s", res.code, res.stdout, res.stderr)
	}
	mcpItems, _ := callMcpTool(t, newMcpTestSession(t, dir), "log", map[string]any{"limit": 200})["items"].([]any)
	for name, kinds := range map[string]map[string]bool{"cli": wpLogKinds(out.Data.Items), "mcp": wpLogKinds(mcpItems)} {
		for _, k := range []string{"workpackage.create", "workpackage.claim", "workpackage.submit", "workpackage.accept"} {
			if !kinds[k] {
				t.Errorf("%s log lacks %s: %v", name, k, kinds)
			}
		}
		if kinds["workpackage.checkpoint"] {
			t.Errorf("%s log shows the checkpoint: %v", name, kinds)
		}
	}
}

func toolSchemaProps(t *testing.T, session *mcp.ClientSession, name string) map[string]any {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != name {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		return schema.Properties
	}
	t.Fatalf("tool %s not registered", name)
	return nil
}

func claimSpan(t *testing.T, c map[string]any) time.Duration {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, c["ts"].(string))
	if err != nil {
		t.Fatal(err)
	}
	until, err := time.Parse(time.RFC3339Nano, c["lease_until"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return until.Sub(ts)
}

// wp_renew and wp_release publish exactly the arguments they store.
func TestMcpWorkpackageRenewReleaseParams(t *testing.T) {
	requireGitBin(t)
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	for name, props := range map[string][]string{
		"wp_renew":   {"id", "claim_id", "lease", "branch"},
		"wp_release": {"id", "claim_id", "message"},
	} {
		got := toolSchemaProps(t, session, name)
		if len(got) != len(props) {
			t.Errorf("%s schema properties %v, want %v", name, got, props)
		}
		for _, p := range props {
			if _, ok := got[p]; !ok {
				t.Errorf("%s schema lacks %q: %v", name, p, got)
			}
		}
	}

	id := callMcpTool(t, session, "wp_new", map[string]any{"title": "WP"})["id"].(string)
	claim := callMcpTool(t, session, "wp_start", map[string]any{"id": id, "branch": "a"})["claim_id"].(string)
	if got := callMcpTool(t, session, "wp_renew", map[string]any{"id": id, "lease": "2h", "branch": "b"})["claim_id"]; got != claim {
		t.Fatalf("renew claim %v want %s", got, claim)
	}
	c := callMcpTool(t, session, "wp_show", map[string]any{"id": id})["meta"].(map[string]any)["claims"].([]any)[0].(map[string]any)
	if span := claimSpan(t, c); c["branch"] != "b" || span < 2*time.Hour || span > 2*time.Hour+time.Minute {
		t.Fatalf("renewed claim %v (span %v)", c, span)
	}
	callMcpTool(t, session, "wp_release", map[string]any{"id": id, "claim_id": claim, "message": "bye"})
	show := callMcpTool(t, session, "wp_show", map[string]any{"id": id})
	c = show["meta"].(map[string]any)["claims"].([]any)[0].(map[string]any)
	notes, _ := show["notes"].([]any)
	if c["released_at"] == nil || len(notes) != 1 || notes[0].(map[string]any)["body"] != "bye" {
		t.Fatalf("release: claim %v notes %v", c, notes)
	}
}

// Lease boundary on both clients: omitted = 24h, 0s and 1ns are
// invalid_input with no write, 1ms is accepted and stored strictly after ts.
func TestCLIAndMCPStartLeaseBoundary(t *testing.T) {
	requireGitBin(t)
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	for _, tc := range []struct {
		name, lease string
		want        time.Duration // 0 means rejected
	}{
		{"omitted", "", 24 * time.Hour},
		{"0s", "0s", 0},
		{"1ns", "1ns", 0},
		{"1ms", "1ms", time.Millisecond},
	} {
		for _, client := range []string{"cli", "mcp"} {
			t.Run(client+"/"+tc.name, func(t *testing.T) {
				id := wpRun(t, dir, 0, "wp", "new", "--title", tc.name).Data["id"].(string)
				ref := "refs/projects/workpackage/" + id
				before := gitIn(t, dir, "rev-parse", ref)
				rejected := false
				if client == "cli" {
					args := []string{"wp", "start", id, "--no-sync", "--json"}
					if tc.lease != "" {
						args = append(args, "--lease", tc.lease)
					}
					res := runCLI(t, dir, args...)
					var out wpOut
					_ = json.Unmarshal([]byte(res.stdout), &out)
					rejected = res.code == 2 && out.Error.Code == "invalid_input"
				} else {
					args := map[string]any{"id": id}
					if tc.lease != "" {
						args["lease"] = tc.lease
					}
					res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "wp_start", Arguments: args})
					if err != nil {
						t.Fatal(err)
					}
					if res.IsError && len(res.Content) > 0 {
						text, _ := res.Content[0].(*mcp.TextContent)
						rejected = text != nil && strings.Contains(text.Text, "lease must be at least 1ms")
					}
				}
				if tc.want == 0 {
					if !rejected {
						t.Fatalf("lease %q was not rejected as invalid_input", tc.lease)
					}
					if gitIn(t, dir, "rev-parse", ref) != before {
						t.Fatal("rejected lease wrote a claim")
					}
					return
				}
				// A 1ms claim may already be claim_expired when Outcome runs;
				// what matters is that it was accepted and stored correctly.
				if rejected {
					t.Fatalf("lease %q rejected", tc.lease)
				}
				claims := wpRun(t, dir, 0, "wp", "show", id).Data["meta"].(map[string]any)["claims"].([]any)
				if len(claims) != 1 {
					t.Fatalf("claims %v", claims)
				}
				if got := claimSpan(t, claims[0].(map[string]any)); got != tc.want {
					t.Fatalf("lease_until - ts = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// Human (non-JSON) output: show/list print derived approval warnings, and a
// failed wp start prints its prefetch_failed warning on stderr before the
// error.
func TestCLIWorkpackageHumanWarnings(t *testing.T) {
	requireGitBin(t)
	dir := newCLITestRepo(t)
	id := wpRun(t, dir, 0, "wp", "new", "--title", "WP").Data["id"].(string)
	wpRun(t, dir, 0, "wp", "submit", id, "--no-commit")
	wpRun(t, dir, 0, "wp", "accept", id) // self-accept, no users registry
	raw, _ := wpRun(t, dir, 0, "wp", "show", id).Data["warnings"].([]any)
	var codes []string
	for _, w := range raw {
		codes = append(codes, w.(string))
	}
	if !strings.Contains(strings.Join(codes, " "), "self_accept") {
		t.Fatalf("fixture: expected self_accept among %v", codes)
	}
	show := runCLI(t, dir, "wp", "show", id, "--no-sync")
	list := runCLI(t, dir, "wp", "list", "--no-sync")
	if show.code != 0 || list.code != 0 {
		t.Fatalf("show %d %s / list %d %s", show.code, show.stderr, list.code, list.stderr)
	}
	for _, code := range codes {
		if !strings.Contains(show.stdout, "warning: "+code+"\n") {
			t.Errorf("show stdout lacks %s:\n%s", code, show.stdout)
		}
	}
	if !strings.Contains(list.stdout, "["+strings.Join(codes, " ")+"]") {
		t.Errorf("list stdout lacks %v:\n%s", codes, list.stdout)
	}

	other := wpRun(t, dir, 0, "wp", "new", "--title", "claimed").Data["id"].(string)
	gitIn(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
	if res := runCLI(t, dir, "wp", "start", other, "--remote", "broken"); res.code != 0 {
		t.Fatalf("first start: %d %s", res.code, res.stderr)
	}
	failed := runCLI(t, dir, "wp", "start", other, "--remote", "broken")
	if failed.code != 3 {
		t.Fatalf("second start exit %d: %s", failed.code, failed.stderr)
	}
	if failed.stdout != "" {
		t.Errorf("failed start wrote stdout: %q", failed.stdout)
	}
	warnAt := strings.Index(failed.stderr, "warning: prefetch_failed: ")
	errAt := strings.Index(failed.stderr, "error: ")
	if warnAt < 0 || errAt < 0 || warnAt > errAt || !strings.Contains(failed.stderr[errAt:], "claimed by") {
		t.Errorf("stderr must carry prefetch_failed before the claim_held error:\n%s", failed.stderr)
	}
}

// wp renew shares the start boundary on both clients: 0s/1ns and an empty
// CLI --lease= are invalid_input with no write; omitted renews to 24h.
func TestCLIAndMCPRenewLeaseBoundary(t *testing.T) {
	requireGitBin(t)
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	id := wpRun(t, dir, 0, "wp", "new", "--title", "WP").Data["id"].(string)
	wpRun(t, dir, 0, "wp", "start", id, "--lease", "1h")
	ref := "refs/projects/workpackage/" + id
	before := gitIn(t, dir, "rev-parse", ref)
	for _, lease := range []string{"--lease=0s", "--lease=1ns", "--lease="} {
		if code := wpRun(t, dir, 2, "wp", "renew", id, lease).Error.Code; code != "invalid_input" {
			t.Fatalf("cli renew %s: %s", lease, code)
		}
	}
	for _, lease := range []string{"0s", "1ns"} {
		callMcpToolExpectError(t, session, "wp_renew", map[string]any{"id": id, "lease": lease})
	}
	if gitIn(t, dir, "rev-parse", ref) != before {
		t.Fatal("rejected renew wrote an event")
	}
	wpRun(t, dir, 0, "wp", "renew", id)
	c := wpRun(t, dir, 0, "wp", "show", id).Data["meta"].(map[string]any)["claims"].([]any)[0].(map[string]any)
	if span := claimSpan(t, c); span < 24*time.Hour || span > 24*time.Hour+time.Minute {
		t.Fatalf("omitted renew lease span %v", span)
	}
}
