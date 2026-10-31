package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCLISyncRejectsOptionRemote is #99 at the CLI: --remote that git would
// parse as an option is refused (exit 2) and its command never runs.
func TestCLISyncRejectsOptionRemote(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	res := runCLI(t, dir, "sync", "--remote=--upload-pack=touch "+marker, "--json")
	if res.code != 2 || errorCode(t, res) != "invalid_remote" {
		t.Fatalf("code=%d stdout=%s stderr=%s, want exit 2 / invalid_remote", res.code, res.stdout, res.stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the injected command ran")
	}
}

// TestMcpSyncRejectsOtherRemote: MCP's sync/doctor only talk to the remote
// the server was started with, whatever the tool argument says.
func TestMcpSyncRejectsOtherRemote(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	session := newMcpTestSession(t, dir)
	callMcpToolExpectError(t, session, "sync", map[string]any{"remote": "--upload-pack=touch " + marker})
	callMcpToolExpectError(t, session, "sync", map[string]any{"remote": "https://attacker.example/x.git"})
	callMcpToolExpectError(t, session, "doctor", map[string]any{"remote": "evil"})
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the injected command ran")
	}
}
