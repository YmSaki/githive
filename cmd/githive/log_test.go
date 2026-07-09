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
