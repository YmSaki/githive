package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// testPubKey is a syntactically valid ed25519 public key (never used to sign).
const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINb/HvyUTIDZYyBZaz6iHIOXxLN881kIXkr6k5mDrDVi laptop"

// TestCLIWhoamiJSONRegisteredWithKeys: a registered user with keys must get
// ok:true and the key list, not an "unsupported type" failure that still
// exits 0 (#126).
func TestCLIWhoamiJSONRegisteredWithKeys(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "users", "add", "cli", "--email", "cli@example.com", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "cli", "--pub", testPubKey, "--no-sync", "--json")

	res := runCLI(t, dir, "whoami", "--json")
	if res.code != 0 {
		t.Fatalf("whoami --json: code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Registered bool             `json:"registered"`
			Username   string           `json:"username"`
			Keys       []map[string]any `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !env.OK || !env.Data.Registered || env.Data.Username != "cli" || len(env.Data.Keys) != 1 {
		t.Fatalf("unexpected whoami result: %s", res.stdout)
	}
}
