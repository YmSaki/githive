package usersapp

import (
	"context"
	"os/exec"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "admin@example.com"}, {"user.name", "Admin"}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return New(dir)
}

func TestAddUserAndKeyLifecycle(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	if err := svc.AddUser(ctx, "yuumiya", "ゆうみや", "staroprog1103@gmail.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	user, err := svc.GetUser(ctx, "yuumiya")
	if err != nil {
		t.Fatal(err)
	}
	if user["display"] != "ゆうみや" {
		t.Errorf("unexpected display: %v", user["display"])
	}

	pub := "ssh-ed25519 AAAA yuumiya@main"
	if err := svc.KeyAdd(ctx, "yuumiya", pub); err != nil {
		t.Fatal(err)
	}
	user, _ = svc.GetUser(ctx, "yuumiya")
	keys, _ := user["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %v", keys)
	}

	if err := svc.KeyRevoke(ctx, "yuumiya", pub); err != nil {
		t.Fatal(err)
	}
	user, _ = svc.GetUser(ctx, "yuumiya")
	keys, _ = user["keys"].([]any)
	key0, _ := keys[0].(map[string]any)
	if key0["revoked_at"] == nil {
		t.Error("expected key to be revoked")
	}
}

func TestKeyOpsOnUnknownUserFail(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.KeyAdd(ctx, "ghost", "ssh-ed25519 AAAA"); err != ErrUserNotFound {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestInvalidUsernameRejected(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "A_bad_name!", "", "", "", nil); err == nil {
		t.Error("expected invalid username to be rejected")
	}
}

func TestGroupLifecycle(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	if err := svc.GroupSet(ctx, "core", []string{"yuumiya", "dev-agent-01"}, "コア開発"); err != nil {
		t.Fatal(err)
	}
	_, groups, _, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0]["name"] != "core" {
		t.Fatalf("unexpected groups: %+v", groups)
	}

	if err := svc.GroupRemove(ctx, "core"); err != nil {
		t.Fatal(err)
	}
	_, groups, _, err = svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("expected group to be removed, got %+v", groups)
	}
}

func TestPolicySetAndList(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	rules := []any{
		map[string]any{"refs": "refs/projects/**", "allow": []any{"role:member"}, "actions": []any{"push"}},
	}
	if err := svc.PolicySet(ctx, rules, "deny"); err != nil {
		t.Fatal(err)
	}
	_, _, policy, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policy["default"] != "deny" {
		t.Errorf("unexpected policy: %+v", policy)
	}
}

func TestResolveToEmail(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	// Already an email: passed through even with an empty registry.
	got, err := svc.ResolveToEmail(ctx, "someone@example.com")
	if err != nil || got != "someone@example.com" {
		t.Fatalf("got %q %v", got, err)
	}

	if err := svc.AddUser(ctx, "yuumiya", "", "staroprog1103@gmail.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	got, err = svc.ResolveToEmail(ctx, "yuumiya")
	if err != nil || got != "staroprog1103@gmail.com" {
		t.Fatalf("got %q %v", got, err)
	}

	if _, err := svc.ResolveToEmail(ctx, "ghost"); err == nil {
		t.Error("expected error resolving unknown username")
	}
}

func TestValidNameHelpers(t *testing.T) {
	if !ValidUsername("yuumiya") || !ValidUsername("dev-agent-01") {
		t.Error("expected valid usernames to pass")
	}
	if ValidUsername("A") || ValidUsername("-abc") || ValidUsername("a") {
		t.Error("expected invalid usernames to fail")
	}
	if !ValidGroupname("core") {
		t.Error("expected valid group name to pass")
	}
}
