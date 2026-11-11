package sign

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/materialize"
)

// TestVerifyCommitRevokedKeyAfterRotation is #110's reproduction with real
// signatures: alice has k1 and k2, k1 is revoked, and a commit signed with
// k1 after the revocation must fail even though k2 is still active.
func TestVerifyCommitRevokedKeyAfterRotation(t *testing.T) {
	requireSSHKeygen(t)
	isolateGitConfig(t)
	k1 := genKeyPair(t, "k1")
	k2 := genKeyPair(t, "k2")
	readPub := func(path string) string {
		b, err := os.ReadFile(path + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	email := "a@example.com"
	dir := newConfiguredRepo(t,
		[2]string{"user.email", email}, [2]string{"user.name", "A"}, [2]string{"gpg.format", "ssh"})

	commitWith := func(key, date string) string {
		cmd := exec.Command("git", "-C", dir, "-c", "user.signingkey="+key, "commit", "-q", "-S", "--allow-empty", "-m", "x")
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
		out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	before := commitWith(k1, "2026-07-01T00:00:00Z")
	after := commitWith(k1, "2026-07-05T00:00:00Z")
	byK2 := commitWith(k2, "2026-07-05T00:00:00Z")

	state := materialize.NewState()
	state.Collections["users"] = map[string]any{
		"alice": map[string]any{
			"username": "alice",
			"emails":   []any{email},
			"keys": []any{
				map[string]any{"pub": readPub(k1), "revoked_at": "2026-07-04T00:00:00.000Z"},
				map[string]any{"pub": readPub(k2), "revoked_at": nil},
			},
		},
	}

	ctx := context.Background()
	for _, c := range []struct {
		name, hash, ts string
		want           bool
	}{
		{"k1 before revocation", before, "2026-07-01T00:00:00.000Z", true},
		{"k1 after revocation", after, "2026-07-05T00:00:00.000Z", false},
		{"k2 after k1's revocation", byK2, "2026-07-05T00:00:00.000Z", true},
	} {
		res, err := VerifyCommit(ctx, dir, c.hash, state, email, c.ts)
		if err != nil {
			t.Fatal(err)
		}
		if res.Valid != c.want {
			t.Errorf("%s: Valid=%v, want %v\n%s", c.name, res.Valid, c.want, res.Output)
		}
	}
}
