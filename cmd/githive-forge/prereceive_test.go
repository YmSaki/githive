// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var forgeBin string

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir, err := os.MkdirTemp("", "githive-forge-bin")
	if err != nil {
		panic(err)
	}
	forgeBin = filepath.Join(dir, "githive-forge")
	if runtime.GOOS == "windows" {
		forgeBin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", forgeBin, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, "git", args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

// setup creates a bare origin whose pre-receive hook runs the built
// githive-forge binary with hookArgs, and a clone with commits base <- child
// plus a sibling of child.
func setup(t *testing.T, hookArgs string) (clone, base, child, sibling string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// forge の結合テストは Linux/macOS 限定（ADR-0019 決定 4）。
		t.Skip("hook scripts are POSIX shell")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	mustGit(t, root, "init", "-q", "--bare", origin)
	script := "#!/bin/sh\nexec '" + forgeBin + "' hook pre-receive " + hookArgs + "\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	clone = filepath.Join(root, "clone")
	mustGit(t, root, "init", "-q", clone)
	mustGit(t, clone, "remote", "add", "origin", origin)
	empty := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	base = mustGit(t, clone, "commit-tree", "-m", "base", empty)
	child = mustGit(t, clone, "commit-tree", "-p", base, "-m", "child", empty)
	sibling = mustGit(t, clone, "commit-tree", "-p", base, "-m", "sibling", empty)
	return clone, base, child, sibling
}

func push(t *testing.T, clone string, refspecs ...string) (string, error) {
	t.Helper()
	return run(t, clone, "git", append([]string{"push", "--porcelain", "origin"}, refspecs...)...)
}

// The objects being pushed exist only in git's quarantine while the hook
// runs, so these cases also check that receiveapp reads them (ADR-0035 決定 1).
func TestCLIPreReceiveFastForwardOnlyUnderProjects(t *testing.T) {
	clone, base, child, sibling := setup(t, "")
	if out, err := push(t, clone, base+":refs/projects/issue/a", base+":refs/heads/main"); err != nil {
		t.Fatalf("initial push refused: %v\n%s", err, out)
	}
	if out, err := push(t, clone, child+":refs/projects/issue/a"); err != nil {
		t.Fatalf("fast-forward refused: %v\n%s", err, out)
	}
	out, err := push(t, clone, "+"+sibling+":refs/projects/issue/a")
	if err == nil || !strings.Contains(out, "rejected refs/projects/issue/a [non_fast_forward]") {
		t.Fatalf("force push to refs/projects accepted or wrong reason: %v\n%s", err, out)
	}
	if got := mustGit(t, clone, "ls-remote", "origin", "refs/projects/issue/a"); !strings.HasPrefix(got, child) {
		t.Fatalf("origin ref moved after refusal: %s", got)
	}
	if out, err := push(t, clone, "+"+sibling+":refs/heads/main"); err != nil {
		t.Fatalf("force push outside refs/projects refused: %v\n%s", err, out)
	}
}

func TestCLIPreReceiveReservedNamespace(t *testing.T) {
	clone, base, _, _ := setup(t, "")
	out, err := push(t, clone, base+":refs/githive-remote/origin/issue/x")
	if err == nil || !strings.Contains(out, "[reserved_namespace]") {
		t.Fatalf("push to refs/githive-remote accepted or wrong reason: %v\n%s", err, out)
	}
}

func TestCLIPreReceiveStrictNamespaceMode(t *testing.T) {
	clone, base, _, _ := setup(t, "--namespace-mode=strict --allow-namespace=refs/notes/")
	if out, err := push(t, clone, base+":refs/heads/main", base+":refs/notes/commits"); err != nil {
		t.Fatalf("allowed namespaces refused: %v\n%s", err, out)
	}
	out, err := push(t, clone, base+":refs/pull/1/head")
	if err == nil || !strings.Contains(out, "rejected refs/pull/1/head [namespace_not_allowed]") {
		t.Fatalf("strict mode accepted refs/pull or wrong reason: %v\n%s", err, out)
	}
}

func TestCLIPreReceiveMisconfiguredRefusesPush(t *testing.T) {
	clone, base, _, _ := setup(t, "--namespace-mode=closed")
	out, err := push(t, clone, base+":refs/heads/main")
	if err == nil || !strings.Contains(out, "pre-receive misconfigured") {
		t.Fatalf("misconfigured hook accepted the push: %v\n%s", err, out)
	}
}
