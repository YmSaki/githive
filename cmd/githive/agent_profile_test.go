package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/usersapp"
)

func TestCLIAgentSetupPreservesRequestedProfile(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen required")
	}
	dir := newCLITestRepo(t)
	res := mustRunCLI(t, dir, "users", "add", "build-agent", "--agent", "--project", "quality", "--key-dir", filepath.Join(t.TempDir(), "keys"), "--display", "Build Agent", "--role", "member,reader", "--no-sync", "--json")
	var output struct {
		Data struct {
			PrivateKey    string `json:"private_key"`
			ConfigSnippet string `json:"config_snippet"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &output); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(output.Data.PrivateKey) || !strings.Contains(output.Data.ConfigSnippet, "git config user.signingkey ") {
		t.Fatalf("missing machine-readable setup instructions: %s", res.stdout)
	}
	data, err := exec.Command("git", "-C", dir, "show", "refs/projects/users/registry:users/build-agent.json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var user map[string]any
	if err := json.Unmarshal(data, &user); err != nil {
		t.Fatal(err)
	}
	if user["kind"] != "agent" || user["display"] != "Build Agent" {
		t.Fatalf("requested profile ignored: %s", data)
	}
	roles, _ := user["roles"].([]any)
	if len(roles) != 2 || roles[0] != "member" || roles[1] != "reader" {
		t.Fatalf("requested roles ignored: %s", data)
	}
}

func TestMCPAgentSetupPreservesRequestedProfile(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen required")
	}
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	data := callMcpTool(t, session, "users_add", map[string]any{
		"name": "mcp-agent", "agent": true, "project": "quality", "key_dir": filepath.Join(t.TempDir(), "keys"),
		"display": "MCP Agent", "roles": []any{"member", "reader"},
	})
	if data["private_key"] == nil || data["config_snippet"] == nil {
		t.Fatalf("missing setup information: %v", data)
	}
	user, err := usersapp.New(dir).GetUser(context.Background(), "mcp-agent")
	if err != nil {
		t.Fatal(err)
	}
	roles, _ := user["roles"].([]any)
	if user["display"] != "MCP Agent" || len(roles) != 2 || roles[0] != "member" || roles[1] != "reader" {
		t.Fatalf("profile ignored: %v", user)
	}
}

func TestInvalidAgentRoleCannotCreateKeysOrAppend(t *testing.T) {
	dir := newCLITestRepo(t)
	keyDir := filepath.Join(t.TempDir(), "not-created")
	result := runCLI(t, dir, "users", "add", "bad-role-agent", "--agent", "--project", "quality", "--key-dir", keyDir, "--role", "automation", "--no-sync", "--json")
	if result.code != 2 || errorCode(t, result) != "invalid_input" {
		t.Fatalf("unsupported role accepted: %+v", result)
	}
	if _, err := os.Stat(keyDir); !os.IsNotExist(err) {
		t.Fatalf("invalid role created keys: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname)", "refs/projects/users/").Output(); err != nil || len(out) != 0 {
		t.Fatalf("invalid role appended: %s %v", out, err)
	}
	session := newMcpTestSession(t, dir)
	callMcpToolExpectError(t, session, "users_add", map[string]any{"name": "bad-role-agent", "agent": true, "project": "quality", "key_dir": keyDir, "roles": []any{"automation"}})
	if _, err := os.Stat(keyDir); !os.IsNotExist(err) {
		t.Fatalf("MCP invalid role created keys: %v", err)
	}
}
