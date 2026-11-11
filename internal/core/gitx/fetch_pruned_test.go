package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Both fetch entry points must keep caller input after the option terminator.
func TestFetchModesRejectOptionRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	marker := filepath.Join(t.TempDir(), "injected")
	r := New(dir)
	for name, fetch := range map[string]func(context.Context, string, ...string) error{"fetch": r.Fetch, "pruned": r.FetchPruned} {
		t.Run(name, func(t *testing.T) {
			err := fetch(context.Background(), "--upload-pack=touch "+marker, "refs/x:refs/x")
			if err == nil {
				t.Fatal("option-shaped remote must be rejected")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unexpected marker stat result: %v", err)
			}
		})
	}
}
