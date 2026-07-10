package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestCLILogJSONShape verifies `githive log --json` follows the shared
// list envelope shape (docs/10-cli-spec.md:68「一覧系は { "items": [...],
// "total": n } で統一する」). This regression is invisible to
// internal/app/logapp's unit tests, which assert on Go values rather than
// the actual CLI process's stdout bytes; PR #8 shipped a "entries" key
// that only an e2e-level test like this one would have caught (issue #10).
func TestCLILogJSONShape(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	// Seed events across three features so the cross-feature merge has
	// something real to report on.
	if res := runCLI(t, dir, "issue", "new", "--title", "t1", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if res := runCLI(t, dir, "task", "new", "--title", "t1", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("task new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if res := runCLI(t, dir, "chat", "new", "--title", "c1", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("chat new failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res := runCLI(t, dir, "log", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("log failed: code=%d stderr=%s", res.code, res.stderr)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &envelope); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !envelope.OK {
		t.Fatalf("expected ok:true, got %s", res.stdout)
	}
	if envelope.Data.Items == nil {
		t.Fatalf("expected an \"items\" array in --json output, got %s", res.stdout)
	}
	if envelope.Data.Total != len(envelope.Data.Items) {
		t.Errorf("total (%d) does not match len(items) (%d)", envelope.Data.Total, len(envelope.Data.Items))
	}
	if envelope.Data.Total < 3 {
		t.Errorf("expected at least 3 events (issue+task+chat), got total=%d", envelope.Data.Total)
	}

	seenFeatures := map[string]bool{}
	for _, item := range envelope.Data.Items {
		feature, _ := item["feature"].(string)
		seenFeatures[feature] = true
	}
	for _, want := range []string{"issue", "task", "chat"} {
		if !seenFeatures[want] {
			t.Errorf("expected an item with feature=%q in --json output, got features=%v", want, seenFeatures)
		}
	}
}

// TestCLILogActorFilterJSON verifies --actor narrows the --json items to
// events by exactly that actor (ADR-0009: actor is always a raw email, no
// username resolution for `githive log`).
func TestCLILogActorFilterJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	if res := runCLI(t, dir, "issue", "new", "--title", "t1", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res := runCLI(t, dir, "log", "--actor", "nobody@example.com", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("log failed: code=%d stderr=%s", res.code, res.stderr)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &envelope); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if envelope.Data.Total != 0 || len(envelope.Data.Items) != 0 {
		t.Errorf("expected zero items for an actor with no events, got total=%d items=%v", envelope.Data.Total, envelope.Data.Items)
	}
}

// TestCLILogInvalidSinceExitCode verifies `githive log --since <invalid>`
// exits with code 2 (usage error, docs/10-cli-spec.md「終了コード」) through
// the actual CLI binary. internal/app/logapp's TestListInvalidSince only
// covers this at the Go-value level (calling logapp.Service.List directly);
// it cannot catch a regression in how cmd/githive/log.go or root.go's error
// classification wires ErrInvalidSince to an exit code.
func TestCLILogInvalidSinceExitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	// Each of these is RFC3339-valid but not the envelope ts format
	// (RFC3339 UTC millisecond precision), matching
	// internal/app/logapp/logapp_test.go's TestListInvalidSince cases.
	invalid := []string{
		"not-a-timestamp",
		"2026-07-09T12:00:00Z",
		"2026-07-09T12:00:00.000+00:00",
	}
	for _, since := range invalid {
		res := runCLI(t, dir, "log", "--since", since, "--no-sync", "--json")
		if res.code != 2 {
			t.Errorf("log --since %q: expected exit code 2, got %d (stdout=%s stderr=%s)", since, res.code, res.stdout, res.stderr)
		}

		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &envelope); err != nil {
			t.Fatalf("log --since %q: bad json: %v\n%s", since, err, res.stdout)
		}
		if envelope.OK {
			t.Errorf("log --since %q: expected ok:false, got %s", since, res.stdout)
		}
	}
}
