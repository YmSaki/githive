package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireSSHSigning skips unless this machine can both produce and verify
// SSH signatures. Signing is optional (ADR-0007), so the suite must stay
// green on hosts with an old or absent openssh (ADR-0014「帰結」).
func requireSSHSigning(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	out, err := exec.Command("ssh-keygen", "-Y", "sign").CombinedOutput()
	if err != nil && strings.Contains(string(out), "unknown operation") {
		t.Skip("ssh-keygen is too old for -Y sign")
	}
}

// newSignedCLITestRepo builds a repo configured exactly the way a user who
// followed `githive doctor`'s signing advice would have it: commit.gpgsign
// on, gpg.format=ssh, user.signingkey pointing at a real key. It returns the
// repo dir, the signer's email and the private key path.
//
// The global/system git config is neutralised first: this test asserts on
// signing behaviour, so it must not inherit whatever the developer running
// it has configured (in particular a gpg.ssh.program shim).
func newSignedCLITestRepo(t *testing.T) (dir, email, keyPath string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	email = "signer@example.com"
	keyPath = filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}

	dir = t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", dir},
		{"-C", dir, "config", "user.email", email},
		{"-C", dir, "config", "user.name", "Signer"},
		{"-C", dir, "config", "commit.gpgsign", "true"},
		{"-C", dir, "config", "gpg.format", "ssh"},
		{"-C", dir, "config", "user.signingkey", keyPath},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir, email, keyPath
}

func mustRunCLI(t *testing.T, dir string, args ...string) cliResult {
	t.Helper()
	res := runCLI(t, dir, args...)
	if res.code != 0 {
		t.Fatalf("githive %v: code=%d stdout=%s stderr=%s", args, res.code, res.stdout, res.stderr)
	}
	return res
}

// TestCLISignedWritesVerifyClean is A1's end-to-end goal: with signing
// configured, every commit githive writes (meta.init, the users registry,
// issue events) carries an SSH signature that `githive verify` accepts
// against the registry, so a healthy repository reports zero issues
// (docs/11-security.md「SSH 署名」, ADR-0014).
func TestCLISignedWritesVerifyClean(t *testing.T) {
	requireSSHSigning(t)
	dir, email, keyPath := newSignedCLITestRepo(t)

	mustRunCLI(t, dir, "init", "--project", "signed", "--json")
	// role:admin as well, so the registry writes below satisfy verify's
	// "registry changes come from an admin" rule and the only thing this
	// test can fail on is the signature check.
	mustRunCLI(t, dir, "users", "add", "signer", "--email", email, "--kind", "human", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "signer", "--pub", keyPath+".pub", "--no-sync", "--json")

	res := mustRunCLI(t, dir, "issue", "new", "--title", "signed issue", "--body", "b", "--no-sync", "--json")
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if created.Data.ID == "" {
		t.Fatalf("no issue id in %s", res.stdout)
	}
	mustRunCLI(t, dir, "issue", "comment", created.Data.ID, "-m", "hello", "--no-sync", "--json")

	// Every githive-written commit must now be signed by the registered key,
	// as judged by plain git rather than by githive's own verifier.
	assertAllCommitsSignedWith(t, dir, email, keyPath+".pub")

	res = runCLI(t, dir, "verify", "--json")
	if res.code != 0 {
		t.Fatalf("githive verify reported issues: code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var verified struct {
		OK   bool `json:"ok"`
		Data struct {
			Reports []struct {
				Ref    string           `json:"ref"`
				Issues []map[string]any `json:"issues"`
			} `json:"reports"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &verified); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !verified.OK {
		t.Fatalf("expected ok:true, got %s", res.stdout)
	}
	if len(verified.Data.Reports) == 0 {
		t.Fatal("expected verify to cover at least one ref")
	}
	for _, r := range verified.Data.Reports {
		if len(r.Issues) != 0 {
			t.Errorf("ref %s reported issues: %+v", r.Ref, r.Issues)
		}
	}
}

// TestCLIUnsignedWritesStillWork guards the other half of the contract:
// without signing configuration, githive writes unsigned commits exactly as
// it did before ADR-0014.
func TestCLIUnsignedWritesStillWork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := newCLITestRepo(t)

	mustRunCLI(t, dir, "issue", "new", "--title", "unsigned", "--no-sync", "--json")
	for _, ref := range gitRefs(t, dir) {
		if sig := commitField(t, dir, ref, "%GK"); sig != "" {
			t.Errorf("ref %s is signed but no signing config was set", ref)
		}
	}
}

// TestCLISigningMisconfiguredFailsLoudly pins ADR-0014's decision that a
// half-configured signing setup is an environment error (exit 5), not a
// silent downgrade to unsigned commits.
func TestCLISigningMisconfiguredFailsLoudly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := newCLITestRepo(t)
	// gpg.gpgsign on, but gpg.format left at git's openpgp default.
	if out, err := exec.Command("git", "-C", dir, "config", "commit.gpgsign", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	res := runCLI(t, dir, "issue", "new", "--title", "t", "--no-sync", "--json")
	if res.code != 5 {
		t.Fatalf("expected exit code 5 (environment), got %d: %s%s", res.code, res.stdout, res.stderr)
	}
	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &failed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if failed.OK || failed.Error.Code != "signing_misconfigured" {
		t.Errorf("expected signing_misconfigured, got %s", res.stdout)
	}
}

// assertAllCommitsSignedWith checks with plain git - not githive's own
// verifier - that every commit on every githive ref carries a signature git
// itself calls good ("%G?" == "G"). This is the interoperability claim
// ADR-0014 makes: go-git's PGPSignature field produces a gpgsig header in
// git's own format.
func assertAllCommitsSignedWith(t *testing.T, dir, principal, pubKeyPath string) {
	t.Helper()
	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed_signers")
	if err := os.WriteFile(allowed, []byte(principal+" "+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}

	refs := gitRefs(t, dir)
	if len(refs) == 0 {
		t.Fatal("no refs/projects refs were created")
	}
	for _, ref := range refs {
		out, err := exec.Command("git", "-C", dir,
			"-c", "gpg.format=ssh",
			"-c", "gpg.ssh.allowedSignersFile="+allowed,
			"log", "--format=%H %G?", ref).Output()
		if err != nil {
			t.Fatalf("git log %s: %v", ref, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("unexpected git log line for %s: %q", ref, line)
			}
			if fields[1] != "G" {
				t.Errorf("ref %s: commit %s has signature status %q, want G", ref, fields[0], fields[1])
			}
		}
	}
}

func gitRefs(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname)", "refs/projects/").Output()
	if err != nil {
		t.Fatalf("git for-each-ref: %v", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func commitField(t *testing.T, dir, ref, format string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format="+format, ref).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}
