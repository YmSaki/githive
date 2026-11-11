package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func policyRegistryRefs(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/").CombinedOutput()
	if err != nil {
		t.Fatalf("list refs: %v: %s", err, out)
	}
	return string(out)
}

func runPolicyEditorCLI(t *testing.T, dir string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "--repo", dir, "--no-sync", "--json", "users", "policy", "edit")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("policy edit did not terminate: %v", ctx.Err())
	}
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestCLIUsersPolicyEditRequiresExplicitEditor(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	t.Setenv("EDITOR", "")
	before := policyRegistryRefs(t, dir)
	res := runPolicyEditorCLI(t, dir)
	if res.code != 2 || errorCode(t, res) != "invalid_policy" || !strings.Contains(res.stdout, "EDITOR") {
		t.Fatalf("unexpected editor error: %+v", res)
	}
	if policyRegistryRefs(t, dir) != before {
		t.Fatal("missing editor changed refs")
	}
}

func TestCLIUsersPolicyEditPreservesFailedDrafts(t *testing.T) {
	requireGitCLI(t)
	if runtime.GOOS == "windows" {
		t.Skip("test editor uses a POSIX shell")
	}
	for _, tc := range []struct {
		name, content string
		editorFails   bool
		writeFails    bool
	}{
		{"editor failure", `{"rules":[],"default":"allow"}`, true, false},
		{"JSON failure", `{"rules":`, false, false},
		{"null policy", `null`, false, false},
		{"null rules", `{"rules":null,"default":"allow"}`, false, false},
		{"invalid default", `{"rules":[],"default":"alow"}`, false, false},
		{"write failure", `{"rules":[],"default":"allow"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newCLITestRepo(t)
			if tc.writeFails {
				if out, err := exec.Command("git", "-C", dir, "config", "commit.gpgsign", "true").CombinedOutput(); err != nil {
					t.Fatalf("configure failure: %v: %s", err, out)
				}
			}
			editor := filepath.Join(t.TempDir(), "editor.sh")
			script := "#!/bin/sh\nprintf '%s' \"$POLICY_TEST_CONTENT\" > \"$1\"\nprintf 'editor output\\n'\nif [ \"$POLICY_TEST_FAIL\" = 1 ]; then exit 7; fi\n"
			if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			drafts := t.TempDir()
			t.Setenv("TMPDIR", drafts)
			t.Setenv("EDITOR", editor)
			t.Setenv("POLICY_TEST_CONTENT", tc.content)
			t.Setenv("POLICY_TEST_FAIL", "0")
			if tc.editorFails {
				t.Setenv("POLICY_TEST_FAIL", "1")
			}
			before := policyRegistryRefs(t, dir)
			res := runPolicyEditorCLI(t, dir)
			if res.code == 0 || !json.Valid([]byte(res.stdout)) || !strings.Contains(res.stderr, "editor output") {
				t.Fatalf("failed edit result: %+v", res)
			}
			files, err := filepath.Glob(filepath.Join(drafts, "githive-policy-*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("draft missing: %v, %v", files, err)
			}
			content, err := os.ReadFile(files[0])
			if err != nil || string(content) != tc.content || !strings.Contains(res.stdout, files[0]) {
				t.Fatalf("edited draft not recoverable: %q, %v, %+v", content, err, res)
			}
			if policyRegistryRefs(t, dir) != before {
				t.Fatal("failed edit changed refs")
			}
		})
	}
}

func TestCLIUsersPolicyEditCleansSuccessfulAndUnchangedDrafts(t *testing.T) {
	requireGitCLI(t)
	if runtime.GOOS == "windows" {
		t.Skip("test editor uses a POSIX shell")
	}
	for _, changed := range []bool{false, true} {
		dir := newCLITestRepo(t)
		editor := filepath.Join(t.TempDir(), "editor.sh")
		script := "#!/bin/sh\nprintf 'editor output\\n'\n"
		if changed {
			script += "printf '%s' '{\"rules\":[],\"default\":\"allow\"}' > \"$1\"\n"
		}
		if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		drafts := t.TempDir()
		t.Setenv("TMPDIR", drafts)
		t.Setenv("EDITOR", editor)
		before := policyRegistryRefs(t, dir)
		res := runPolicyEditorCLI(t, dir)
		if res.code != 0 || !json.Valid([]byte(res.stdout)) || !strings.Contains(res.stderr, "editor output") {
			t.Fatalf("successful edit: %+v", res)
		}
		files, _ := filepath.Glob(filepath.Join(drafts, "githive-policy-*.json"))
		if len(files) != 0 {
			t.Fatalf("successful edit retained drafts: %v", files)
		}
		if !changed && policyRegistryRefs(t, dir) != before {
			t.Fatal("unchanged edit appended an event")
		}
		if changed && policyRegistryRefs(t, dir) == before {
			t.Fatal("changed policy was not saved")
		}
	}
}

func TestMcpUsersPolicySetRejectsInvalidPolicyWithoutWriting(t *testing.T) {
	requireGitCLI(t)
	dir := newCLITestRepo(t)
	session := newMcpTestSession(t, dir)
	before := policyRegistryRefs(t, dir)
	callMcpToolExpectError(t, session, "users_policy_set", map[string]any{
		"rules": []any{map[string]any{"refs": "refs/**", "allow": []any{}, "actions": []any{"pussh"}}}, "default": "allow",
	})
	if policyRegistryRefs(t, dir) != before {
		t.Fatal("invalid MCP policy changed refs")
	}
	callMcpTool(t, session, "users_policy_set", map[string]any{"rules": []any{}, "default": "deny"})
}
