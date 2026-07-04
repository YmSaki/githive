package initapp

import (
	"context"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "tester@example.com"}, {"user.name", "Tester"}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	// Give the repo a remote (even an unreachable one) so remote.origin.fetch
	// can be configured, mirroring a real `git clone`.
	cmd = exec.Command("git", "-C", dir, "remote", "add", "origin", "https://example.invalid/repo.git")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	return dir
}

func TestInitIsIdempotent(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	result, err := Init(ctx, dir, "origin", "githive")
	if err != nil {
		t.Fatal(err)
	}
	if !result.RefspecAdded || !result.LogAllRefUpdates || !result.MetaConfigMade {
		t.Fatalf("expected first init to do everything, got %+v", result)
	}

	r := gitx.New(dir)
	values, err := r.ConfigGetAll(ctx, "remote.origin.fetch")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range values {
		if v == "+refs/projects/*:refs/githive-remote/*" {
			found = true
		}
	}
	if !found {
		t.Errorf("tracking refspec not configured: %v", values)
	}

	logAll, err := r.ConfigGet(ctx, "core.logAllRefUpdates")
	if err != nil {
		t.Fatal(err)
	}
	if logAll != "always" {
		t.Errorf("expected core.logAllRefUpdates=always, got %q", logAll)
	}

	oid, err := r.RevParse(ctx, "refs/projects/meta/config")
	if err != nil {
		t.Fatal(err)
	}
	if oid == "" {
		t.Fatal("expected refs/projects/meta/config to exist after init")
	}

	// Second run must be a no-op (idempotent).
	result2, err := Init(ctx, dir, "origin", "githive")
	if err != nil {
		t.Fatal(err)
	}
	if result2.RefspecAdded || result2.MetaConfigMade {
		t.Errorf("expected second init to be a no-op, got %+v", result2)
	}

	valuesAfter, err := r.ConfigGetAll(ctx, "remote.origin.fetch")
	if err != nil {
		t.Fatal(err)
	}
	if len(valuesAfter) != len(values) {
		t.Errorf("expected refspec not to be duplicated: %v", valuesAfter)
	}

	oid2, err := r.RevParse(ctx, "refs/projects/meta/config")
	if err != nil {
		t.Fatal(err)
	}
	if oid2 != oid {
		t.Errorf("expected meta/config to be untouched by second init, got %s vs %s", oid2, oid)
	}
}
