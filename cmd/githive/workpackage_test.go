package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type wpOut struct {
	OK       bool             `json:"ok"`
	Data     map[string]any   `json:"data"`
	Warnings []map[string]any `json:"warnings"`
	Error    struct {
		Code string         `json:"code"`
		Data map[string]any `json:"data"`
	} `json:"error"`
}

func wpExec(t *testing.T, dir string, wantCode int, args ...string) wpOut {
	t.Helper()
	res := runCLI(t, dir, append(args, "--json")...)
	if res.code != wantCode {
		t.Fatalf("%v: exit %d want %d\nstdout=%s\nstderr=%s", args, res.code, wantCode, res.stdout, res.stderr)
	}
	var out wpOut
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, res.stdout)
	}
	return out
}

func wpRun(t *testing.T, dir string, wantCode int, args ...string) wpOut {
	t.Helper()
	return wpExec(t, dir, wantCode, append(args, "--no-sync")...)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCLIWorkpackageLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	id := wpRun(t, dir, 0, "workpackage", "new", "--title", "WP", "--body", "acceptance").Data["id"].(string)
	dep := wpRun(t, dir, 0, "wp", "new", "--title", "Dep").Data["id"].(string)
	wpRun(t, dir, 0, "wp", "dep", id, dep)
	if code := wpRun(t, dir, 2, "wp", "dep", dep, id).Error.Code; code != "invalid_input" {
		t.Fatalf("cycle: %s", code)
	}
	show := wpRun(t, dir, 0, "wp", "show", id).Data
	for _, k := range []string{"derived_status", "blocked_reasons", "active_claim", "verified", "incomplete_inputs", "observation_scope", "read_source", "history_complete", "stale_approval", "warnings"} {
		if _, ok := show[k]; !ok {
			t.Fatalf("show lacks %q: %v", k, show)
		}
	}
	if show["derived_status"] != "Blocked" || show["read_source"] != "chain" || show["observation_scope"] != "local_refs_only" {
		t.Fatalf("show %v", show)
	}
	if list := wpRun(t, dir, 0, "wp", "list", "--ready").Data; list["total"] != float64(1) || list["history_complete"] != true {
		t.Fatalf("ready list %v", list)
	}
	wpRun(t, dir, 0, "wp", "cancel", dep)
	reasons := wpRun(t, dir, 0, "wp", "show", id).Data["blocked_reasons"].([]any)
	if len(reasons) != 1 || reasons[0].(map[string]any)["type"] != "cancelled_dependency" {
		t.Fatalf("cancelled dep reasons %v", reasons)
	}
	wpRun(t, dir, 0, "wp", "dep", id, dep, "--remove")
	started := wpRun(t, dir, 0, "wp", "start", id, "--branch", "feat/x").Data
	if started["acquired"] != true || started["published"] != false || started["claim_id"] == "" {
		t.Fatalf("start %v", started)
	}
	if code := wpRun(t, dir, 3, "wp", "start", id).Error.Code; code != "claim_held" {
		t.Fatalf("second start: %s", code)
	}
	wpRun(t, dir, 0, "wp", "submit", id, "--no-commit", "-m", "done")
	gitIn(t, dir, "config", "user.email", "rev@example.com")
	wpRun(t, dir, 0, "wp", "accept", id)
	if st := wpRun(t, dir, 0, "wp", "show", id).Data["derived_status"]; st != "Completed" {
		t.Fatalf("after accept %v", st)
	}
	gitIn(t, dir, "config", "user.email", "cli@example.com")
	wpRun(t, dir, 0, "wp", "edit", id, "--body", "changed")
	show = wpRun(t, dir, 0, "wp", "show", id).Data
	if show["derived_status"] != "InReview" || show["stale_approval"] != true {
		t.Fatalf("after edit %v", show)
	}
	gitIn(t, dir, "config", "user.email", "rev@example.com")
	if code := wpRun(t, dir, 3, "wp", "accept", id).Error.Code; code != "stale_acceptance_version" {
		t.Fatalf("re-accept: %s", code)
	}
	if code := wpRun(t, dir, 1, "wp", "show", "zzzzzzzzzz").Error.Code; code != "not_found" {
		t.Fatalf("missing: %s", code)
	}
}

func TestCLIWorkpackageStartRemoteHandling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	id := wpRun(t, dir, 0, "wp", "new", "--title", "WP").Data["id"].(string)
	before := gitIn(t, dir, "rev-parse", "refs/projects/workpackage/"+id)
	if code := wpExec(t, dir, 2, "wp", "start", id, "--remote", "nosuch").Error.Code; code != "invalid_remote" {
		t.Fatalf("invalid remote: %s", code)
	}
	if gitIn(t, dir, "rev-parse", "refs/projects/workpackage/"+id) != before {
		t.Fatal("invalid remote wrote a claim")
	}
	gitIn(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
	out := wpExec(t, dir, 0, "wp", "start", id, "--remote", "broken")
	if out.Data["published"] != false || !hasWarning(out.Warnings, "prefetch_failed") || !hasWarning(out.Warnings, "sync_failed") {
		t.Fatalf("broken remote: %+v", out)
	}
	for _, w := range out.Warnings {
		if w["code"] == "prefetch_failed" && w["data"].(map[string]any)["remote"] != "broken" {
			t.Fatalf("prefetch_failed must name the selected remote: %v", w)
		}
	}
	// The error path carries the prefetch warning in error.data.warnings.
	failed := wpExec(t, dir, 3, "wp", "start", id, "--remote", "broken")
	ws, _ := failed.Error.Data["warnings"].([]any)
	found := false
	for _, w := range ws {
		if m, ok := w.(map[string]any); ok && m["code"] == "prefetch_failed" {
			found = true
		}
	}
	if failed.Error.Code != "claim_held" || !found {
		t.Fatalf("error warnings: %+v", failed.Error)
	}
}

func hasWarning(ws []map[string]any, code string) bool {
	for _, w := range ws {
		if w["code"] == code {
			return true
		}
	}
	return false
}

// §11-6/§11-9: in a shallow clone reads fall back to snapshots per row and
// mutations needing the history refuse with shallow_history.
func TestCLIWorkpackageShallowClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	source := newCLITestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, source, "init", "--bare", origin)
	gitIn(t, source, "commit", "--allow-empty", "-m", "seed")
	gitIn(t, source, "branch", "-M", "main")
	gitIn(t, source, "remote", "add", "origin", origin)
	gitIn(t, source, "push", "origin", "main")
	gitIn(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	deep := wpRun(t, source, 0, "wp", "new", "--title", "deep").Data["id"].(string)
	wpRun(t, source, 0, "wp", "note", deep, "-m", "second event")
	ghost := "01kzzzzzzzzzzzzzzzzzzzzzzz"
	wpRun(t, source, 0, "wp", "new", "--title", "single")
	gitIn(t, source, "push", "origin", "refs/projects/*:refs/projects/*")

	dir := filepath.Join(t.TempDir(), "shallow")
	gitIn(t, source, "clone", "--depth", "1", "file://"+origin, dir)
	gitIn(t, dir, "fetch", "--depth", "1", "origin", "+refs/projects/*:refs/projects/*")
	gitIn(t, dir, "config", "user.email", "cli@example.com")
	gitIn(t, dir, "remote", "remove", "origin")

	list := wpRun(t, dir, 0, "wp", "list")
	if list.Data["history_complete"] != false || list.Data["total"] != float64(2) {
		t.Fatalf("shallow list %v", list.Data)
	}
	sources := map[string]any{}
	for _, it := range list.Data["items"].([]any) {
		m := it.(map[string]any)
		sources[m["id"].(string)] = m["read_source"]
	}
	if sources[deep] != "snapshot" {
		t.Fatalf("per-row read_source %v", sources)
	}
	if show := wpRun(t, dir, 0, "wp", "show", deep).Data; show["read_source"] != "snapshot" || show["history_complete"] != false {
		t.Fatalf("shallow show %v", show)
	}
	before := gitIn(t, dir, "rev-parse", "refs/projects/workpackage/"+deep)
	for _, args := range [][]string{{"wp", "start", deep}, {"wp", "dep", deep, ghost, "--remove"}} {
		if code := wpRun(t, dir, 5, args...).Error.Code; code != "shallow_history" {
			t.Fatalf("%v: %s", args, code)
		}
	}
	if gitIn(t, dir, "rev-parse", "refs/projects/workpackage/"+deep) != before {
		t.Fatal("shallow mutation moved the ref")
	}
}
