package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These e2e tests cover the cmd/githive/root.go wiring for the P4 write
// commands (fsck registration + classifyError exit codes) that the app-layer
// unit tests cannot reach: they run the actual CLI binary and assert on its
// process exit code and JSON envelope.

func TestCLIFsckCleanRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	// Seed one entity so fsck has a real chain to validate.
	if res := runCLI(t, dir, "issue", "new", "--title", "t1", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	res := runCLI(t, dir, "fsck", "--json")
	if res.code != 0 {
		t.Fatalf("fsck on a clean repo should exit 0, got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var env struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("fsck --json bad json: %v\n%s", err, res.stdout)
	}
	if !env.OK {
		t.Errorf("expected ok:true from clean fsck, got %s", res.stdout)
	}
}

// TestCLIWikiSaveValidationExit4 verifies the new classifyError path: a wiki
// save whose worktree contains a non-portable filename (Windows reserved name)
// exits with code 4 (verify-failed) and error.code "wiki_validation_failed",
// with the violations in error.data — before any commit/merge/push.
func TestCLIWikiSaveValidationExit4(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "wiki", "edit", "--json")
	if res.code != 0 {
		t.Fatalf("wiki edit failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var editEnv struct {
		Data struct {
			Worktree string `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &editEnv); err != nil {
		t.Fatalf("wiki edit --json bad json: %v\n%s", err, res.stdout)
	}
	if editEnv.Data.Worktree == "" {
		t.Fatalf("wiki edit did not return a worktree path: %s", res.stdout)
	}

	// Introduce a Windows-reserved filename (CON.md) into the worktree.
	if err := os.WriteFile(filepath.Join(editEnv.Data.Worktree, "CON.md"), []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res = runCLI(t, dir, "wiki", "save", "-m", "add reserved name", "--no-sync", "--json")
	if res.code != 4 {
		t.Fatalf("wiki save with a reserved filename should exit 4, got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
			Data struct {
				Violations []map[string]any `json:"violations"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("wiki save --json bad json: %v\n%s", err, res.stdout)
	}
	if env.OK || env.Error.Code != "wiki_validation_failed" {
		t.Errorf("expected ok:false wiki_validation_failed, got %s", res.stdout)
	}
	if len(env.Error.Data.Violations) == 0 {
		t.Errorf("expected violations in error.data, got %s", res.stdout)
	}
}
