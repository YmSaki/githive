package usersapp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
}

func TestAddAgent(t *testing.T) {
	requireGit(t)
	requireSSHKeygen(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "admin@example.com"}, {"user.name", "Admin"}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}

	keyDir := filepath.Join(t.TempDir(), "keys")
	setup, err := AddAgent(context.Background(), dir, "dev-agent-01", "myproj", "", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Email != "dev-agent-01@agents.myproj.invalid" {
		t.Errorf("unexpected minted email: %q", setup.Email)
	}
	if setup.PubLine == "" {
		t.Error("expected a non-empty public key line")
	}

	svc := New(dir)
	user, err := svc.GetUser(context.Background(), "dev-agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if user["kind"] != "agent" {
		t.Errorf("expected kind=agent, got %v", user["kind"])
	}
	keys, _ := user["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("expected the generated key to be registered, got %v", keys)
	}
}
