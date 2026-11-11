package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestValidateRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://example.com/x.git").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := New(dir)
	ctx := context.Background()
	for remote, ok := range map[string]bool{
		"origin":                           true,
		"https://example.com/x.git":        true,
		"git@example.com:x.git":            true,
		"/srv/git/x.git":                   true,
		"../x.git":                         true,
		"upstream":                         false, // name-shaped but not configured
		"":                                 false,
		"--upload-pack=touch /tmp/pwned":   false,
		"-oProxyCommand=x":                 false,
		"origin x":                         false,
		"https://example.com/x.git\n--foo": false,
	} {
		err := r.ValidateRemote(ctx, remote)
		if ok && err != nil || !ok && !errors.Is(err, ErrInvalidRemote) {
			t.Errorf("ValidateRemote(%q) = %v, want ok=%v", remote, err, ok)
		}
	}
}

// TestFetchPushLsRemoteRejectOptionRemote is #99: a remote that git would
// parse as --upload-pack/--receive-pack must never run the command.
func TestFetchPushLsRemoteRejectOptionRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	marker := filepath.Join(t.TempDir(), "pwned")
	r := New(dir)
	ctx := context.Background()
	evilFetch := "--upload-pack=touch " + marker
	evilPush := "--receive-pack=touch " + marker
	if err := r.Fetch(ctx, evilFetch, "refs/x:refs/x"); !errors.Is(err, ErrInvalidRemote) {
		t.Errorf("Fetch: want ErrInvalidRemote, got %v", err)
	}
	if _, err := r.Push(ctx, evilPush, "refs/x:refs/x"); !errors.Is(err, ErrInvalidRemote) {
		t.Errorf("Push: want ErrInvalidRemote, got %v", err)
	}
	if _, err := r.lsRemoteRef(ctx, evilFetch, "refs/x"); !errors.Is(err, ErrInvalidRemote) {
		t.Errorf("ls-remote: want ErrInvalidRemote, got %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the injected command ran")
	}
	if _, err := r.RevParse(ctx, "--git-dir"); !errors.Is(err, ErrInvalidRef) {
		t.Errorf("RevParse: want ErrInvalidRef, got %v", err)
	}
}
