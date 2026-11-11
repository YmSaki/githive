package initapp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
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
	// Give the repo a reachable remote, mirroring a real `git clone`.
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "remote", "add", "origin", origin)
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

// TestInitUnreachableRemoteDoesNotCreateRoot: when the initial fetch fails,
// init cannot know whether the remote already holds the project, so it must
// not mint a meta.init root of its own (issue #101).
func TestInitUnreachableRemoteDoesNotCreateRoot(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	r := gitx.New(dir)
	if err := r.ConfigSet(ctx, "remote.origin.url", filepath.Join(t.TempDir(), "missing.git")); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(ctx, dir, "origin", "p"); err == nil {
		t.Fatal("expected init to fail when the remote cannot be fetched")
	}
	oid, err := r.RevParse(ctx, "refs/projects/meta/config")
	if err != nil {
		t.Fatal(err)
	}
	if oid != "" {
		t.Error("init created meta/config without knowing the remote's state")
	}
}

// TestInitWithoutRemote: no remote means no url-less remote.<name>.fetch
// entry (which would break a later `git remote add`), a warning, and a
// local-only project.
func TestInitWithoutRemote(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	if out, err := exec.Command("git", "-C", dir, "remote", "remove", "origin").CombinedOutput(); err != nil {
		t.Fatalf("git remote remove: %v\n%s", err, out)
	}

	result, err := Init(ctx, dir, "origin", "p")
	if err != nil {
		t.Fatal(err)
	}
	if result.RefspecAdded || result.Remote.Configured || len(result.Warnings) == 0 {
		t.Errorf("unexpected result for a repo without a remote: %+v", result)
	}
	if !result.MetaConfigMade {
		t.Error("expected meta/config to be created for a local-only project")
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", filepath.Join(t.TempDir(), "o.git")).CombinedOutput(); err != nil {
		t.Errorf("git remote add after init failed: %v\n%s", err, out)
	}
}

// TestInitChecksIdentityBeforeWriting: a missing identity must fail init
// before any git config is written.
func TestInitChecksIdentityBeforeWriting(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "empty"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, key := range []string{"user.email", "user.name"} {
		if out, err := exec.Command("git", "-C", dir, "config", "--unset", key).CombinedOutput(); err != nil {
			t.Fatalf("git config --unset %s: %v\n%s", key, err, out)
		}
	}

	if _, err := Init(ctx, dir, "origin", "p"); err == nil {
		t.Fatal("expected init to fail without an identity")
	}
	out, _ := exec.Command("git", "-C", dir, "config", "--get-all", "remote.origin.fetch").Output()
	if strings.Contains(string(out), "refs/githive-remote") {
		t.Errorf("init wrote the tracking refspec before failing: %s", out)
	}
}
