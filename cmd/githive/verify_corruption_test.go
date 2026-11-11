package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestCLIAndMcpVerifyPreserveReportsWhenOneEnvelopeIsMalformed(t *testing.T) {
	dir := newCLITestRepo(t)
	good := createdID(t, mustRunCLI(t, dir, "issue", "new", "--title", "good", "--no-sync", "--json").stdout)
	bad := createdID(t, mustRunCLI(t, dir, "issue", "new", "--title", "bad", "--no-sync", "--json").stdout)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	ref := "refs/projects/issue/" + bad
	old := git("rev-parse", ref)
	tree := git("rev-parse", ref+"^{tree}")
	corrupt := git("commit-tree", tree, "-p", old, "-m", "broken\n\n{not json")
	git("update-ref", ref, corrupt, old)
	goodOID := git("rev-parse", "refs/projects/issue/"+good)
	git("update-ref", "refs/projects/future/item", goodOID)
	result := runCLI(t, dir, "verify", "--json")
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
			Data struct {
				Reports []map[string]any `json:"reports"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("bad JSON: %v %s", err, result.stdout)
	}
	if result.code != 4 || payload.OK || payload.Error.Code != "verify_failed" {
		t.Fatalf("malformed chain has wrong exit/error: %+v", result)
	}
	check := func(reports []map[string]any) {
		t.Helper()
		goodSeen, badSeen, warnSeen := false, false, false
		for _, report := range reports {
			switch report["ref"] {
			case "refs/projects/issue/" + good:
				goodSeen = true
			case ref:
				for _, raw := range report["issues"].([]any) {
					badSeen = badSeen || raw.(map[string]any)["code"] == "invalid_envelope"
				}
			case "refs/projects/future/item":
				warnSeen = report["warnings"].([]any)[0].(map[string]any)["code"] == "unrecognized_ref"
			}
		}
		if !goodSeen || !badSeen || !warnSeen {
			t.Fatalf("partial report lost: %+v", reports)
		}
	}
	check(payload.Error.Data.Reports)
	session := newMcpTestSession(t, dir)
	data := callMcpTool(t, session, "verify", map[string]any{})
	if data["ok"] != false {
		t.Fatalf("MCP incorrectly passed: %+v", data)
	}
	var reports []map[string]any
	for _, raw := range data["reports"].([]any) {
		reports = append(reports, raw.(map[string]any))
	}
	check(reports)
}
