package fsckapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/chatapp"
)

// requireSSHSigning skips unless openssh can sign; signing is optional
// (ADR-0007) so this test must not hard-fail without it.
func requireSSHSigning(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	out, err := exec.Command("ssh-keygen", "-Y", "sign").CombinedOutput()
	if err != nil && strings.Contains(string(out), "unknown operation") {
		t.Skip("ssh-keygen is too old for -Y sign")
	}
}

// TestCheckpointCommitIsSigned closes the last gap in ADR-0014's coverage:
// `githive fsck --compact` writes through entitychain.Writer like every
// other feature write, so its checkpoint commits must be signed too.
func TestCheckpointCommitIsSigned(t *testing.T) {
	requireGit(t)
	requireSSHSigning(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	const email = "tester@example.com"
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}

	dir := newTestRepo(t)
	for _, kv := range [][2]string{
		{"commit.gpgsign", "true"},
		{"gpg.format", "ssh"},
		{"user.signingkey", keyPath},
	} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", kv[0], err, out)
		}
	}

	ctx := context.Background()
	svc := chatapp.New(dir)
	id, err := svc.NewThread(ctx, "signed thread", "first")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"second", "third"} {
		if err := svc.Post(ctx, id, body, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	report, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checkpoints) == 0 {
		t.Fatal("expected fsck --compact to append a checkpoint")
	}

	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed_signers")
	if err := os.WriteFile(allowed, []byte(email+" "+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}

	ref := "refs/projects/chat/" + id
	out, err := exec.Command("git", "-C", dir,
		"-c", "gpg.format=ssh",
		"-c", "gpg.ssh.allowedSignersFile="+allowed,
		"log", "--format=%H %G? %s", ref).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", ref, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sawCheckpoint := false
	for _, line := range lines {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			t.Fatalf("unexpected git log line: %q", line)
		}
		if fields[1] != "G" {
			t.Errorf("commit %s (%s) has signature status %q, want G", fields[0], fields[2], fields[1])
		}
		if strings.HasPrefix(fields[2], "chat.checkpoint") {
			sawCheckpoint = true
		}
	}
	if !sawCheckpoint {
		t.Errorf("no checkpoint commit found on %s:\n%s", ref, out)
	}
}
