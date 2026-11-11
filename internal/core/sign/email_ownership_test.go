package sign

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestVerifyCommitRejectsKeyFromAnotherUserSharingAdminEmail(t *testing.T) {
	requireSSHKeygen(t)
	isolateGitConfig(t)
	adminKey, agentKey := genKeyPair(t, "admin"), genKeyPair(t, "agent")
	email := "admin@example.com"
	dir := newConfiguredRepo(t, [2]string{"user.email", email}, [2]string{"user.name", "Admin"}, [2]string{"gpg.format", "ssh"})
	cmd := exec.Command("git", "-C", dir, "-c", "user.signingkey="+agentKey, "commit", "-q", "-S", "--allow-empty", "-m", "agent acting as admin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	hash, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	pub := func(key string) string {
		t.Helper()
		b, err := os.ReadFile(key + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	state := materialize.NewState()
	state.Collections["users"] = map[string]any{
		"admin": map[string]any{"emails": []any{email}, "roles": []any{"admin"}, "keys": []any{map[string]any{"pub": pub(adminKey), "revoked_at": nil}}},
		"agent": map[string]any{"emails": []any{email}, "roles": []any{}, "keys": []any{map[string]any{"pub": pub(agentKey), "revoked_at": nil}}},
	}
	result, err := VerifyCommit(context.Background(), dir, strings.TrimSpace(string(hash)), state, email, "2026-11-11T00:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatalf("agent key authenticated duplicated admin email: %+v", result)
	}
	if materialize.IsAdmin(state, email) {
		t.Fatal("ambiguous email inherited admin role")
	}
}

func TestVerifyCommitAcceptsSameUserEmailAlias(t *testing.T) {
	requireSSHKeygen(t)
	isolateGitConfig(t)
	key := genKeyPair(t, "alias")
	email := "second@example.com"
	dir := newConfiguredRepo(t, [2]string{"user.email", email}, [2]string{"user.name", "Owner"}, [2]string{"gpg.format", "ssh"})
	if out, err := exec.Command("git", "-C", dir, "-c", "user.signingkey="+key, "commit", "-q", "-S", "--allow-empty", "-m", "legitimate alias").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	hash, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	state := materialize.NewState()
	state.Collections["users"] = map[string]any{
		"owner": map[string]any{"emails": []any{"first@example.com", email}, "roles": []any{"admin"}, "keys": []any{map[string]any{"pub": strings.TrimSpace(string(pub)), "revoked_at": nil}}},
	}
	result, err := VerifyCommit(context.Background(), dir, strings.TrimSpace(string(hash)), state, email, "2026-11-11T00:00:00.000Z")
	if err != nil || !result.Valid {
		t.Fatalf("legitimate alias denied: %+v %v", result, err)
	}
}
