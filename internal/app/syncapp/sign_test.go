package syncapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
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

func genSigningKey(t *testing.T, email string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	return path
}

func configureSigning(t *testing.T, dir, keyPath string) {
	t.Helper()
	for _, kv := range [][2]string{
		{"commit.gpgsign", "true"},
		{"gpg.format", "ssh"},
		{"user.signingkey", keyPath},
	} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", kv[0], err, out)
		}
	}
}

// TestMergeCommitIsSigned covers the one commit kind no feature write
// produces: the event-union merge commit sync creates when two clones
// diverge. ADR-0014 requires it to be signed like any other, otherwise a
// repository that syncs would fail `githive verify` even though every
// write was signed.
func TestMergeCommitIsSigned(t *testing.T) {
	requireGit(t)
	requireSSHSigning(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	const emailA, emailB = "a@example.com", "b@example.com"
	keyA := genSigningKey(t, emailA)
	keyB := genSigningKey(t, emailB)

	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, emailA)
	dirB := cloneRepo(t, origin, emailB)
	configureSigning(t, dirA, keyA)
	configureSigning(t, dirB, keyB)
	ctx := context.Background()

	svcA := issueapp.New(dirA)
	id, err := svcA.NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	if _, err := Sync(ctx, dirA, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("A initial sync: %v", err)
	}
	if _, err := Sync(ctx, dirB, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("B initial sync: %v", err)
	}

	// Diverge, then let A push first so B has to merge.
	svcB := issueapp.New(dirB)
	if err := svcA.Label(ctx, id, []string{"from-a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svcB.Label(ctx, id, []string{"from-b"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	results, err := Sync(ctx, dirB, "origin", []string{ref}, 5)
	if err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionMerged {
		t.Fatalf("expected B's sync to merge, got %+v", results)
	}

	// Both signers must be trusted: the merge commit is B's, but its
	// ancestors were written by A.
	allowed := filepath.Join(t.TempDir(), "allowed_signers")
	var body strings.Builder
	for _, pair := range [][2]string{{emailA, keyA}, {emailB, keyB}} {
		pub, err := os.ReadFile(pair[1] + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString(pair[0] + " " + string(pub))
	}
	if err := os.WriteFile(allowed, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("git", "-C", dirB,
		"-c", "gpg.format=ssh",
		"-c", "gpg.ssh.allowedSignersFile="+allowed,
		"log", "--format=%H %G? %p", ref).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", ref, err)
	}
	sawMerge := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 2 {
			t.Fatalf("unexpected git log line: %q", line)
		}
		if fields[1] != "G" {
			t.Errorf("commit %s has signature status %q, want G", fields[0], fields[1])
		}
		if len(fields) == 3 && len(strings.Fields(fields[2])) == 2 {
			sawMerge = true
		}
	}
	if !sawMerge {
		t.Errorf("no merge commit found on %s:\n%s", ref, out)
	}
}
