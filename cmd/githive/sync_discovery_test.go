package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCLISyncDiscoversRemoteOnlyEntities is a regression test for a bug
// found in review: `githive sync` used to only look at refs this clone
// already knew about locally, so an entity created purely by another clone
// (an issue/notify post this clone never touched) could never be
// discovered or fast-forwarded in - sync would silently report nothing to
// do. Fixed by having refsToSync fetch first and union local refs with
// whatever appears under refs/githive-remote/*
// (internal/core/refspace.LocalRefFromTracking).
func TestCLISyncDiscoversRemoteOnlyEntities(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	for _, dir := range []string{a, b} {
		if out, err := exec.Command("git", "clone", "--quiet", origin, dir).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		setCLIIdentity(t, dir, dir+"@example.com")
	}

	// a creates an issue; issue new auto-syncs (pushes) by default.
	res := runCLI(t, a, "issue", "new", "--title", "b never saw this", "--json")
	if res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}

	// b has never heard of this issue; sync must discover and pull it in.
	res = runCLI(t, b, "sync", "--json")
	if res.code != 0 {
		t.Fatalf("sync failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, b, "issue", "list", "--json")
	if res.code != 0 {
		t.Fatalf("issue list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 1 || listed.Data.Items[0]["id"] != created.Data.ID {
		t.Fatalf("expected b to discover a's issue via sync, got %+v", listed.Data.Items)
	}
}

// TestCLISyncDiscoversRemoteOnlyNotify is the notify-feature analogue: a
// singleton ref (refs/projects/notify/stream) another clone wrote to must
// also be discoverable by a clone that never had it locally.
func TestCLISyncDiscoversRemoteOnlyNotify(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	for _, dir := range []string{a, b} {
		if out, err := exec.Command("git", "clone", "--quiet", origin, dir).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		setCLIIdentity(t, dir, dir+"@example.com")
	}

	res := runCLI(t, a, "notify", "post", "--to", "all", "--title", "from-a", "--json")
	if res.code != 0 {
		t.Fatalf("notify post failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, b, "sync", "--json")
	if res.code != 0 {
		t.Fatalf("sync failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, b, "notify", "list", "--all", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected b to discover a's notify post via sync, got %+v", listed.Data.Items)
	}
}
