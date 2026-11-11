package usersapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentConfigSnippetPreservesLiteralShellMetacharacters(t *testing.T) {
	requireGit(t)
	requireSSHKeygen(t)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell required")
	}
	svc := newTestRepo(t)
	dir := svc.Dir
	marker := filepath.Join(t.TempDir(), "injected")
	// The email is a valid literal value for the registry. Executing the
	// displayed setup commands must not interpret it as a shell program.
	email := "qa$(touch${IFS}" + filepath.ToSlash(marker) + ")@example.com"
	keyDir := filepath.Join(t.TempDir(), "keys with ' quote $ and ` mark")
	setup, err := AddAgent(context.Background(), dir, "safe-agent", "quality", email, keyDir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", setup.ConfigSnippet)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snippet: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell substitution executed: %v", err)
	}
	for key, want := range map[string]string{"user.email": email, "user.name": "safe-agent", "user.signingkey": setup.PrivateKey} {
		out, err := exec.Command("git", "-C", dir, "config", "--null", "--get", key).Output()
		if err != nil || strings.TrimSuffix(string(out), "\x00") != want {
			t.Fatalf("%s: got %q err=%v want %q", key, out, err, want)
		}
	}
}
