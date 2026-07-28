package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ambiguousTestIDs are three synthetic, syntactically-valid IDs (26-char
// lowercase Crockford32, docs/02-data-model.md「ID：ULID」) that share a
// 10-character prefix. Two real idgen.New() IDs only collide on a shared
// prefix if generated within the same millisecond (internal/core/idgen: ULID
// time component is millisecond-resolution) and from the same process
// (idgen's monotonic entropy source is per-process), so hoping for that
// naturally would make the ambiguous-id tests flaky. Seeding refs by hand
// (seedAmbiguousRefs) makes the collision deterministic instead.
var ambiguousTestIDs = []string{
	"01hqx7z9c2a3f7h2j9m0pqr7s7",
	"01hqx7z9c2a3f7h2j9m0pqr7s8",
	"01hqx7z9c2a3f7h2j9m0pqr7s9",
}

// ambiguousTestPrefix is the shared prefix of ambiguousTestIDs, long enough
// (>= 8 chars) for ResolveID to treat it as a resolvable-by-prefix id rather
// than rejecting it as too short (docs/10-cli-spec.md「ID の入力解決」).
const ambiguousTestPrefix = "01hqx7z9c2"

// seedAmbiguousRefs points refs/projects/<feature>/<id> at one throwaway
// empty commit for each of ambiguousTestIDs, so the feature's ResolveID
// sees more than one match for ambiguousTestPrefix and returns
// *apperr.AmbiguousIDError. ResolveID's ambiguous branch only lists ref
// names (Service.listIDs) - it never reads the commit's tree - so which
// commit the refs point at does not matter, only that one exists.
func seedAmbiguousRefs(t *testing.T, dir, feature string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "seed").CombinedOutput(); err != nil {
		t.Fatalf("git commit --allow-empty: %v\n%s", err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	head := strings.TrimSpace(string(out))
	for _, id := range ambiguousTestIDs {
		ref := "refs/projects/" + feature + "/" + id
		if out, err := exec.Command("git", "-C", dir, "update-ref", ref, head).CombinedOutput(); err != nil {
			t.Fatalf("git update-ref %s: %v\n%s", ref, err, out)
		}
	}
}

// TestCLIAmbiguousIDExitsTwoWithCandidatesJSON is the A5 goal for the CLI's
// --json path, across all three features that resolve short id prefixes:
// exit code 2 (docs/10-cli-spec.md「終了コード」使い方の誤り) and
// error.data.candidates listing every match, for issue/task/chat alike -
// previously only issueapp's ambiguity was recognized by
// cmd/githive/root.go's classifyError (it caught *issueapp.AmbiguousIDError
// specifically), so task/chat fell through to the default "error" branch
// (exit 1, no candidates).
func TestCLIAmbiguousIDExitsTwoWithCandidatesJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	for _, feature := range []string{"issue", "task", "chat"} {
		t.Run(feature, func(t *testing.T) {
			dir := newCLITestRepo(t)
			seedAmbiguousRefs(t, dir, feature)

			res := runCLI(t, dir, feature, "show", ambiguousTestPrefix, "--no-sync", "--json")
			if res.code != 2 {
				t.Fatalf("expected exit code 2 for ambiguous id, got %d (stdout=%s stderr=%s)", res.code, res.stdout, res.stderr)
			}
			var env struct {
				OK    bool `json:"ok"`
				Error struct {
					Code string `json:"code"`
					Data struct {
						Candidates []string `json:"candidates"`
					} `json:"data"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
				t.Fatalf("bad json: %v\n%s", err, res.stdout)
			}
			if env.OK || env.Error.Code != "ambiguous_id" {
				t.Fatalf("expected ok:false error.code=ambiguous_id, got %s", res.stdout)
			}
			if len(env.Error.Data.Candidates) != len(ambiguousTestIDs) {
				t.Fatalf("expected %d candidates, got %+v", len(ambiguousTestIDs), env.Error.Data.Candidates)
			}
			for _, id := range ambiguousTestIDs {
				if !containsString(env.Error.Data.Candidates, id) {
					t.Errorf("expected candidate %s in %+v", id, env.Error.Data.Candidates)
				}
			}
		})
	}
}

// TestCLIAmbiguousIDHumanOutputListsCandidates covers the same three
// features' non-JSON path: docs/10-cli-spec.md requires the candidate list
// to be displayed (「曖昧なら候補一覧を表示して終了コード 2」), not just a
// count, so this asserts every candidate id actually appears in stderr.
func TestCLIAmbiguousIDHumanOutputListsCandidates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	for _, feature := range []string{"issue", "task", "chat"} {
		t.Run(feature, func(t *testing.T) {
			dir := newCLITestRepo(t)
			seedAmbiguousRefs(t, dir, feature)

			res := runCLI(t, dir, feature, "show", ambiguousTestPrefix, "--no-sync")
			if res.code != 2 {
				t.Fatalf("expected exit code 2 for ambiguous id, got %d (stdout=%s stderr=%s)", res.code, res.stdout, res.stderr)
			}
			for _, id := range ambiguousTestIDs {
				if !strings.Contains(res.stderr, id) {
					t.Errorf("expected human-readable error to list candidate %s, got stderr=%q", id, res.stderr)
				}
			}
		})
	}
}

// TestMcpAmbiguousIDReturnsCandidatesInErrorText confirms the MCP path
// (cmd/githive/mcp.go) an Agent client would see: *_show tool handlers just
// return the ResolveID error as-is, and the go-sdk turns any returned error
// into a CallToolResult with IsError=true and the error's Error() text as
// content (no structured error envelope exists for any error kind in this
// codebase's MCP layer, ambiguous id or otherwise) - so the fix that makes
// apperr.AmbiguousIDError's Error() spell out the full candidate list (not
// just a count) is what actually gets the candidates to an MCP client too.
func TestMcpAmbiguousIDReturnsCandidatesInErrorText(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tools := map[string]string{"issue": "issue_show", "task": "task_show", "chat": "chat_show"}
	for feature, tool := range tools {
		t.Run(feature, func(t *testing.T) {
			dir := newCLITestRepo(t)
			seedAmbiguousRefs(t, dir, feature)
			session := newMcpTestSession(t, dir)

			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: tool, Arguments: map[string]any{"id": ambiguousTestPrefix},
			})
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			if !res.IsError {
				t.Fatalf("%s: expected IsError for an ambiguous id, got %+v", tool, res)
			}
			var text string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			for _, id := range ambiguousTestIDs {
				if !strings.Contains(text, id) {
					t.Errorf("%s: expected error text to include candidate %s, got %q", tool, id, text)
				}
			}
		})
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
