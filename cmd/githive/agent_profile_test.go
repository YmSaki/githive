package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCLIAgentSetupPreservesRequestedProfile(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen required")
	}
	dir := newCLITestRepo(t)
	res := mustRunCLI(t, dir, "users", "add", "build-agent", "--agent", "--project", "quality", "--key-dir", filepath.Join(t.TempDir(), "keys"), "--display", "Build Agent", "--role", "member,automation", "--no-sync", "--json")
	var output struct {
		Data struct {
			PrivateKey    string `json:"private_key"`
			ConfigSnippet string `json:"config_snippet"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &output); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(output.Data.PrivateKey) || !strings.Contains(output.Data.ConfigSnippet, strconv.Quote(output.Data.PrivateKey)) {
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
	if len(roles) != 2 || roles[0] != "member" || roles[1] != "automation" {
		t.Fatalf("requested roles ignored: %s", data)
	}
}
