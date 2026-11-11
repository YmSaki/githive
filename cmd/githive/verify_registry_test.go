package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func genSigningKey(t *testing.T, email string) string {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", email, "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	return keyPath
}

// actAs points the repo's committer identity and signing key at someone
// else, the way a second person working in their own clone would be set up.
func actAs(t *testing.T, dir, email, keyPath string) {
	t.Helper()
	for _, kv := range [][2]string{{"user.email", email}, {"user.name", email}, {"user.signingkey", keyPath}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
}

type registryVerifyIssue struct {
	Commit string `json:"commit"`
	Code   string `json:"code"`
}

// registryIssues runs `githive verify --ref <registry> --json` and returns
// the exit code and the registry report's issue codes.
func registryIssues(t *testing.T, dir string) (int, []registryVerifyIssue) {
	t.Helper()
	res := runCLI(t, dir, "verify", "--ref", "refs/projects/users/registry", "--json")
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Reports []struct {
				Issues []registryVerifyIssue `json:"issues"`
			} `json:"reports"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
			Data struct {
				Reports []struct {
					Issues []registryVerifyIssue `json:"issues"`
				} `json:"reports"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	reports := env.Data.Reports
	if !env.OK {
		if env.Error.Code != "verify_failed" {
			t.Fatalf("unexpected failure: %s", res.stdout)
		}
		reports = env.Error.Data.Reports
	}
	var out []registryVerifyIssue
	for _, r := range reports {
		out = append(out, r.Issues...)
	}
	return res.code, out
}

func countCode(issues []registryVerifyIssue, code string) int {
	n := 0
	for _, i := range issues {
		if i.Code == code {
			n++
		}
	}
	return n
}

func setupSignedAdmin(t *testing.T) (dir string) {
	t.Helper()
	requireSSHSigning(t)
	dir, email, keyPath := newSignedCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "signed", "--json")
	mustRunCLI(t, dir, "users", "add", "signer", "--email", email, "--kind", "human", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "signer", "--pub", keyPath+".pub", "--no-sync", "--json")
	return dir
}

// TestCLIVerifyRegistryLegitAdminPasses: changes made by a user who held
// role:admin at the time pass rule 4.
func TestCLIVerifyRegistryLegitAdminPasses(t *testing.T) {
	dir := setupSignedAdmin(t)
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--no-sync", "--json")
	if code, issues := registryIssues(t, dir); code != 0 || len(issues) != 0 {
		t.Fatalf("code=%d issues=%+v", code, issues)
	}
}

// TestCLIVerifyRegistrySelfPromotionDetected is #109's attack: an
// unregistered user signs commits that make themselves admin and register
// their key. Judged against HEAD's registry everything looked fine; judged
// against the registry as of each change, the user_set is not by an admin.
func TestCLIVerifyRegistrySelfPromotionDetected(t *testing.T) {
	dir := setupSignedAdmin(t)
	malloryKey := genSigningKey(t, "mallory@example.com")
	actAs(t, dir, "mallory@example.com", malloryKey)
	mustRunCLI(t, dir, "users", "add", "mallory", "--email", "mallory@example.com", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "mallory", "--pub", malloryKey+".pub", "--no-sync", "--json")

	code, issues := registryIssues(t, dir)
	if code != 4 {
		t.Fatalf("want exit 4, got %d (%+v)", code, issues)
	}
	if n := countCode(issues, "registry_change_without_admin"); n != 1 {
		t.Fatalf("want exactly the self-promoting user_set flagged, got %+v", issues)
	}
}

// TestCLIVerifyRegistryDemotionKeepsPastChangesValid: once bob is demoted,
// the registry changes bob made while still admin must not be reported.
func TestCLIVerifyRegistryDemotionKeepsPastChangesValid(t *testing.T) {
	dir := setupSignedAdmin(t)
	adminEmail := "signer@example.com"
	var adminKey string
	if out, err := exec.Command("git", "-C", dir, "config", "user.signingkey").Output(); err == nil {
		adminKey = string(out[:len(out)-1])
	}
	bobKey := genSigningKey(t, "bob@example.com")
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--role", "admin", "--no-sync", "--json")
	mustRunCLI(t, dir, "users", "key", "add", "bob", "--pub", bobKey+".pub", "--no-sync", "--json")

	actAs(t, dir, "bob@example.com", bobKey)
	mustRunCLI(t, dir, "users", "add", "carol", "--email", "carol@example.com", "--no-sync", "--json")

	actAs(t, dir, adminEmail, adminKey)
	mustRunCLI(t, dir, "users", "add", "bob", "--role", "member", "--no-sync", "--json")

	if code, issues := registryIssues(t, dir); code != 0 || len(issues) != 0 {
		t.Fatalf("code=%d issues=%+v", code, issues)
	}
}

// TestCLIVerifyUnregisteredKeySignatureRejected: with keyed, otherwise
// clean history, a commit signed by a key the registry doesn't know is the
// only thing reported - so the test fails if verify ever accepts an
// invalid signature, not because of some unrelated setup gap.
func TestCLIVerifyUnregisteredKeySignatureRejected(t *testing.T) {
	dir := setupSignedAdmin(t)
	if code, issues := registryIssues(t, dir); code != 0 || len(issues) != 0 {
		t.Fatalf("baseline must be clean: code=%d issues=%+v", code, issues)
	}
	adminKey, err := exec.Command("git", "-C", dir, "config", "user.signingkey").Output()
	if err != nil {
		t.Fatal(err)
	}
	actAs(t, dir, "signer@example.com", genSigningKey(t, "rogue"))
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--no-sync", "--json")
	actAs(t, dir, "signer@example.com", string(adminKey[:len(adminKey)-1]))

	code, issues := registryIssues(t, dir)
	if code != 4 || len(issues) != 1 || issues[0].Code != "invalid_or_missing_signature" {
		t.Fatalf("want exactly one invalid_or_missing_signature, got code=%d issues=%+v", code, issues)
	}
}

// TestCLIVerifyTrustRootPinMismatch: a pin naming some other commit is
// reported as trust_root_mismatch on an otherwise clean, keyed registry.
// The unpinned root then also loses its rule-4 exemption, so it is the
// only commit reported.
func TestCLIVerifyTrustRootPinMismatch(t *testing.T) {
	dir := setupSignedAdmin(t)
	if out, err := exec.Command("git", "-C", dir, "config", "githive.trust.root", "0123456789abcdef0123456789abcdef01234567").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	code, issues := registryIssues(t, dir)
	if code != 4 || countCode(issues, "trust_root_mismatch") != 1 {
		t.Fatalf("want trust_root_mismatch, got code=%d issues=%+v", code, issues)
	}
	for _, i := range issues {
		if i.Commit != issues[0].Commit {
			t.Fatalf("only the root commit should be reported, got %+v", issues)
		}
	}
}
