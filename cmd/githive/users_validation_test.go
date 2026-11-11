package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func errorCode(t *testing.T, res cliResult) string {
	t.Helper()
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	return env.Error.Code
}

// TestCLIUsersKeyAddRejectsNonPublicKeys is #127: a private key file, a
// path that doesn't exist and a garbage string must all be rejected before
// they reach the registry (whose history can never be cleaned).
func TestCLIUsersKeyAddRejectsNonPublicKeys(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "users", "add", "alice", "--email", "alice@example.com", "--no-sync", "--json")

	privPath := filepath.Join(t.TempDir(), "id_ed25519")
	priv := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ==\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(privPath, []byte(priv), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ pub, code string }{
		{privPath, "private_key_rejected"},
		{filepath.Join(t.TempDir(), "nosuchfile.pub"), "invalid_key"},
		{"garbage not a key", "invalid_key"},
		{"ssh-ed25519 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "invalid_key"},
		{"ssh-rsa " + strings.Fields(testPubKey)[1], "invalid_key"},
	} {
		res := runCLI(t, dir, "users", "key", "add", "alice", "--pub", c.pub, "--no-sync", "--json")
		if res.code != 2 || errorCode(t, res) != c.code {
			t.Errorf("key add %q: code=%d stdout=%s, want exit 2 / %s", c.pub, res.code, res.stdout, c.code)
		}
	}
	out, err := exec.Command("git", "-C", dir, "log", "--format=%B", "refs/projects/users/registry").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "PRIVATE KEY") || strings.Contains(string(out), "key_add") {
		t.Fatalf("registry history must not contain a rejected key:\n%s", out)
	}

	// A real public key file and a literal key line still work.
	pubPath := filepath.Join(t.TempDir(), "k.pub")
	if err := os.WriteFile(pubPath, []byte(testPubKey+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunCLI(t, dir, "users", "key", "add", "alice", "--pub", pubPath, "--no-sync", "--json")
}

func TestCLIUsersAddRejectsInvalidEmail(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	res := runCLI(t, dir, "users", "add", "carol", "--email", "carol smith", "--no-sync", "--json")
	if res.code != 2 || errorCode(t, res) != "invalid_email" {
		t.Fatalf("code=%d stdout=%s, want exit 2 / invalid_email", res.code, res.stdout)
	}
}

// TestCLIUsersKeyRevokeIgnoresComment is #89's CLI reproduction: revoking
// with the bare key must revoke the key registered with a comment, and
// revoking a key that isn't registered must fail with not_found instead of
// reporting ok:true.
func TestCLIUsersKeyRevokeIgnoresComment(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "users", "add", "alice", "--email", "alice@example.com", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "alice", "--pub", testPubKey, "--no-sync", "--json") // with comment
	bare := strings.Join(strings.Fields(testPubKey)[:2], " ")
	mustRunCLI(t, dir, "users", "key", "revoke", "alice", "--pub", bare, "--no-sync", "--json")

	out, err := exec.Command("git", "-C", dir, "show", "refs/projects/users/registry:users/alice.json").CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %v\n%s", err, out)
	}
	if strings.Contains(string(out), `"revoked_at": null`) {
		t.Fatalf("key should be revoked:\n%s", out)
	}

	res := runCLI(t, dir, "users", "key", "revoke", "alice", "--pub", bare, "--no-sync", "--json")
	if res.code == 0 || errorCode(t, res) != "not_found" {
		t.Fatalf("revoking an already-revoked key: code=%d stdout=%s, want not_found", res.code, res.stdout)
	}
}

// TestMcpUsersRejectsPrivateKeyAndBadEmail: the same validation applies on
// the MCP side, since it lives in usersapp (#127).
func TestMcpUsersRejectsPrivateKeyAndBadEmail(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	callMcpToolExpectError(t, session, "users_add", map[string]any{"name": "carol", "email": "carol smith"})
	callMcpTool(t, session, "users_add", map[string]any{"name": "alice", "email": "alice@example.com"})
	before, err := exec.Command("git", "-C", dir, "rev-parse", "refs/projects/users/registry").Output()
	if err != nil {
		t.Fatal(err)
	}
	callMcpToolExpectError(t, session, "users_key_add", map[string]any{"name": "alice", "pub": "ssh-ed25519 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	after, err := exec.Command("git", "-C", dir, "rev-parse", "refs/projects/users/registry").Output()
	if err != nil || string(before) != string(after) {
		t.Fatalf("malformed wire key changed registry: %v", err)
	}
	callMcpToolExpectError(t, session, "users_key_add", map[string]any{"name": "alice", "pub": "-----BEGIN OPENSSH PRIVATE KEY----- abc"})
	callMcpTool(t, session, "users_key_add", map[string]any{"name": "alice", "pub": testPubKey})
	callMcpTool(t, session, "users_key_revoke", map[string]any{"name": "alice", "pub": strings.Join(strings.Fields(testPubKey)[:2], " ")})
	callMcpToolExpectError(t, session, "users_key_revoke", map[string]any{"name": "alice", "pub": testPubKey})
}
