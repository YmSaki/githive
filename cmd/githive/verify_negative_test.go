package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are the negative half of TestCLISignedWritesVerifyClean: each
// one breaks exactly one of docs/11-security.md「検証規則」 and checks that
// `githive verify` reports it under the matching code. Every case also checks
// that a ref which does not break the rule stays clean, so a verifier that
// flags everything cannot pass.

type verifyIssue struct {
	Code   string `json:"code"`
	Commit string `json:"commit"`
}

// verifyCodes runs `githive verify --json` and returns, per ref, the issues
// it reported. A clean run (exit 0) yields an empty map.
func verifyCodes(t *testing.T, dir string) map[string][]verifyIssue {
	t.Helper()
	res := runCLI(t, dir, "verify", "--json")
	var reports []struct {
		Ref    string        `json:"ref"`
		Issues []verifyIssue `json:"issues"`
	}
	var out struct {
		OK   bool `json:"ok"`
		Data struct {
			Reports json.RawMessage `json:"reports"`
		} `json:"data"`
		Error struct {
			Data struct {
				Reports json.RawMessage `json:"reports"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("bad verify json: %v\n%s", err, res.stdout)
	}
	raw := out.Data.Reports
	if !out.OK {
		raw = out.Error.Data.Reports
	}
	if err := json.Unmarshal(raw, &reports); err != nil {
		t.Fatalf("bad verify reports: %v\n%s", err, res.stdout)
	}
	if out.OK != (res.code == 0) {
		t.Fatalf("verify ok=%v but exit code %d", out.OK, res.code)
	}
	byRef := map[string][]verifyIssue{}
	for _, r := range reports {
		if len(r.Issues) > 0 {
			byRef[r.Ref] = r.Issues
		}
	}
	return byRef
}

func hasCode(issues []verifyIssue, code string) bool {
	for _, is := range issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

func refWithPrefix(t *testing.T, dir, prefix string) string {
	t.Helper()
	for _, ref := range gitRefs(t, dir) {
		if strings.HasPrefix(ref, prefix) {
			return ref
		}
	}
	t.Fatalf("no ref with prefix %s", prefix)
	return ""
}

func gitMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSignedRegisteredRepo is newSignedCLITestRepo plus the registry entries
// that make the signer's own writes verify clean: an admin user carrying
// the signing key.
func newSignedRegisteredRepo(t *testing.T) (dir, email string) {
	t.Helper()
	dir, email, keyPath := newSignedCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "signed", "--json")
	mustRunCLI(t, dir, "users", "add", "signer", "--email", email, "--kind", "human", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "signer", "--pub", keyPath+".pub", "--no-sync", "--json")
	return dir, email
}

// An unsigned commit by a user who has a registered key must be flagged
// (検証規則 1). The committer here owns an active key, so only the
// cryptographic check itself can catch it.
func TestCLIVerifyFlagsUnsignedCommitByKeyHolder(t *testing.T) {
	requireSSHSigning(t)
	dir, _ := newSignedRegisteredRepo(t)

	gitMust(t, dir, "config", "commit.gpgsign", "false")
	mustRunCLI(t, dir, "issue", "new", "--title", "unsigned", "--no-sync", "--json")

	got := verifyCodes(t, dir)
	issueRef := refWithPrefix(t, dir, "refs/projects/issue/")
	if !hasCode(got[issueRef], "invalid_or_missing_signature") {
		t.Errorf("unsigned issue commit not flagged: %+v", got)
	}
	if issues := got["refs/projects/users/registry"]; len(issues) != 0 {
		t.Errorf("signed registry ref should stay clean, got %+v", issues)
	}
}

// A commit signed with a key that is not in the registry must be flagged
// even though the committer email is registered with another key.
func TestCLIVerifyFlagsSignatureFromUnregisteredKey(t *testing.T) {
	requireSSHSigning(t)
	dir, email := newSignedRegisteredRepo(t)

	otherKey := filepath.Join(t.TempDir(), "id_other")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", otherKey).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	gitMust(t, dir, "config", "user.signingkey", otherKey)
	mustRunCLI(t, dir, "issue", "new", "--title", "wrong key", "--no-sync", "--json")

	got := verifyCodes(t, dir)
	issueRef := refWithPrefix(t, dir, "refs/projects/issue/")
	if !hasCode(got[issueRef], "invalid_or_missing_signature") {
		t.Errorf("commit signed by an unregistered key not flagged: %+v", got)
	}
	if issues := got["refs/projects/users/registry"]; len(issues) != 0 {
		t.Errorf("registry ref signed with the registered key should stay clean, got %+v", issues)
	}
}

// An event whose actor differs from its commit's committer is actor
// spoofing (検証規則 2). githive never writes one, so the commit is rebuilt
// by hand with plain git under a different committer.
func TestCLIVerifyFlagsActorCommitterMismatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "issue", "new", "--title", "honest", "--no-sync", "--json")
	mustRunCLI(t, dir, "task", "new", "--title", "spoofed", "--no-sync", "--json")

	taskRef := refWithPrefix(t, dir, "refs/projects/task/")
	msg := gitMust(t, dir, "log", "-1", "--format=%B", taskRef)
	tree := gitMust(t, dir, "rev-parse", taskRef+"^{tree}")
	cmd := exec.Command("git", "-C", dir, "commit-tree", tree, "-m", msg)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_EMAIL=mallory@example.com", "GIT_COMMITTER_NAME=Mallory")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git commit-tree: %v", err)
	}
	gitMust(t, dir, "update-ref", taskRef, strings.TrimSpace(string(out)))

	got := verifyCodes(t, dir)
	if !hasCode(got[taskRef], "actor_committer_mismatch") {
		t.Errorf("spoofed actor not flagged: %+v", got)
	}
	issueRef := refWithPrefix(t, dir, "refs/projects/issue/")
	if hasCode(got[issueRef], "actor_committer_mismatch") {
		t.Errorf("honest issue commit wrongly flagged: %+v", got[issueRef])
	}
}

// A registry change by a user without role:admin must be flagged
// (検証規則 4), while the admin's own registry changes are not.
func TestCLIVerifyFlagsRegistryChangeByNonAdmin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "p", "--json")
	mustRunCLI(t, dir, "users", "add", "cli", "--email", "cli@example.com", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--role", "member", "--no-sync", "--json")

	gitMust(t, dir, "config", "user.email", "bob@example.com")
	mustRunCLI(t, dir, "users", "add", "carol", "--email", "carol@example.com", "--no-sync", "--json")
	bobCommit := gitMust(t, dir, "rev-parse", "refs/projects/users/registry")

	got := verifyCodes(t, dir)
	var flagged []string
	for _, is := range got["refs/projects/users/registry"] {
		if is.Code == "registry_change_without_admin" {
			flagged = append(flagged, is.Commit)
		}
	}
	if len(flagged) != 1 || flagged[0] != bobCommit {
		t.Errorf("expected exactly bob's commit %s flagged, got %v (all: %+v)", bobCommit, flagged, got)
	}
}

// githive.trust.root pins the registry's root commit (docs/11-security.md
// 「信頼の根」): a pin that does not match is reported, the right pin is not.
func TestCLIVerifyTrustRootPin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "p", "--json")
	mustRunCLI(t, dir, "users", "add", "cli", "--email", "cli@example.com", "--role", "admin", "--no-sync", "--json")

	const registry = "refs/projects/users/registry"
	gitMust(t, dir, "config", "githive.trust.root", strings.Repeat("0", 40))
	if got := verifyCodes(t, dir); !hasCode(got[registry], "trust_root_mismatch") {
		t.Errorf("wrong trust root pin not flagged: %+v", got)
	}

	root := gitMust(t, dir, "rev-list", "--max-parents=0", registry)
	gitMust(t, dir, "config", "githive.trust.root", root)
	if got := verifyCodes(t, dir); hasCode(got[registry], "trust_root_mismatch") {
		t.Errorf("correct trust root pin flagged: %+v", got[registry])
	}
}
