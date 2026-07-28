package sign

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireSSHKeygen skips when openssh's ssh-keygen is missing or predates
// `-Y sign` (OpenSSH 8.2p1). Signing is optional, so githive must stay
// usable without it and its tests must not hard-fail (ADR-0014「帰結」).
func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	out, err := exec.Command("ssh-keygen", "-Y", "sign").CombinedOutput()
	// With no arguments this always fails; what distinguishes a usable
	// ssh-keygen is that it recognises the -Y sign operation at all.
	if err != nil && strings.Contains(string(out), "unknown operation") {
		t.Skip("ssh-keygen is too old for -Y sign")
	}
}

// isolateGitConfig points git at empty global/system config so the
// developer's own signing setup cannot leak into these tests.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// genKeyPair writes a passphrase-less ed25519 keypair and returns the
// private key path (what user.signingkey normally points at).
func genKeyPair(t *testing.T, comment string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -t ed25519: %v\n%s", err, out)
	}
	return path
}

func newConfiguredRepo(t *testing.T, kv ...[2]string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, pair := range kv {
		if out, err := exec.Command("git", "-C", dir, "config", pair[0], pair[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", pair[0], err, out)
		}
	}
	return dir
}

// TestResolveSignerOffWhenGpgsignUnset is the default path: no signing
// configuration means no signer and no error, so writes stay unsigned
// exactly as before (ADR-0007: hosted モードでは署名は推奨であって必須で
// はない).
func TestResolveSignerOffWhenGpgsignUnset(t *testing.T) {
	isolateGitConfig(t)
	dir := newConfiguredRepo(t)
	signer, cleanup, err := ResolveSigner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if signer != nil {
		t.Error("expected no signer when commit.gpgsign is unset")
	}
}

func TestResolveSignerOffWhenGpgsignFalse(t *testing.T) {
	isolateGitConfig(t)
	dir := newConfiguredRepo(t, [2]string{"commit.gpgsign", "false"})
	signer, cleanup, err := ResolveSigner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if signer != nil {
		t.Error("expected no signer when commit.gpgsign is false")
	}
}

// TestResolveSignerRejectsNonSSHFormat and the next test pin ADR-0014's
// decision to fail loudly rather than silently downgrade to an unsigned
// commit when signing was asked for but cannot be done.
func TestResolveSignerRejectsNonSSHFormat(t *testing.T) {
	isolateGitConfig(t)
	for _, format := range []string{"", "openpgp", "x509"} {
		kv := [][2]string{{"commit.gpgsign", "true"}}
		if format != "" {
			kv = append(kv, [2]string{"gpg.format", format})
		}
		dir := newConfiguredRepo(t, kv...)
		_, cleanup, err := ResolveSigner(context.Background(), dir)
		cleanup()
		if !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("gpg.format=%q: expected ErrUnsupportedFormat, got %v", format, err)
		}
	}
}

func TestResolveSignerRequiresSigningKey(t *testing.T) {
	isolateGitConfig(t)
	dir := newConfiguredRepo(t,
		[2]string{"commit.gpgsign", "true"},
		[2]string{"gpg.format", "ssh"},
	)
	_, cleanup, err := ResolveSigner(context.Background(), dir)
	cleanup()
	if !errors.Is(err, ErrSigningKeyMissing) {
		t.Fatalf("expected ErrSigningKeyMissing, got %v", err)
	}
}

// TestResolveSignerProducesVerifiableSignature is the end-to-end check that
// what we hand to go-git is a real SSH signature openssh accepts back.
func TestResolveSignerProducesVerifiableSignature(t *testing.T) {
	requireSSHKeygen(t)
	isolateGitConfig(t)
	key := genKeyPair(t, "signer@example.com")
	dir := newConfiguredRepo(t,
		[2]string{"commit.gpgsign", "true"},
		[2]string{"gpg.format", "ssh"},
		[2]string{"user.signingkey", key},
	)

	signer, cleanup, err := ResolveSigner(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if signer == nil {
		t.Fatal("expected a signer when signing is fully configured")
	}

	payload := []byte("tree deadbeef\nauthor a <a@example.com> 0 +0000\n\nmessage\n")
	sig, err := signer(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sig, "-----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("expected an armored SSH signature, got %q", sig)
	}
	assertSignatureVerifies(t, key+".pub", "signer@example.com", payload, sig)
}

// TestSignBufferHonoursExplicitProgram covers the gpg.ssh.program path by
// naming ssh-keygen explicitly; git lets users redirect signing this way and
// githive follows the same setting (ADR-0014).
func TestSignBufferHonoursExplicitProgram(t *testing.T) {
	requireSSHKeygen(t)
	key := genKeyPair(t, "signer@example.com")
	payload := []byte("payload bytes\n")
	sig, err := SignBuffer(context.Background(), "ssh-keygen", key, payload)
	if err != nil {
		t.Fatal(err)
	}
	assertSignatureVerifies(t, key+".pub", "signer@example.com", payload, sig)
}

func TestSignBufferReportsProgramFailure(t *testing.T) {
	_, err := SignBuffer(context.Background(), "ssh-keygen", filepath.Join(t.TempDir(), "missing"), []byte("x"))
	if err == nil {
		t.Fatal("expected an error when the signing key does not exist")
	}
}

// assertSignatureVerifies checks sig with `ssh-keygen -Y verify`, the same
// operation `git verify-commit` performs internally.
func assertSignatureVerifies(t *testing.T, pubKeyPath, principal string, payload []byte, sig string) {
	t.Helper()
	dir := t.TempDir()

	pub, err := os.ReadFile(pubKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte(principal+" "+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "payload.sig")
	if err := os.WriteFile(sigPath, []byte(sig), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", principal, "-n", "git", "-s", sigPath)
	cmd.Stdin = strings.NewReader(string(payload))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y verify: %v\n%s", err, out)
	}
}

func TestResolveKeyFileLiteralPublicKey(t *testing.T) {
	const literal = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample signer@example.com"
	for _, value := range []string{literal, "key::" + literal} {
		path, cleanup, err := resolveKeyFile(value)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			cleanup()
			t.Fatalf("%q: %v", value, err)
		}
		if strings.TrimSpace(string(body)) != literal {
			t.Errorf("%q: temp key file holds %q", value, body)
		}
		cleanup()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q: cleanup did not remove the temp key file", value)
		}
	}
}

func TestResolveKeyFilePathIsLeftAlone(t *testing.T) {
	path, cleanup, err := resolveKeyFile("/home/someone/.ssh/id_ed25519")
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/home/someone/.ssh/id_ed25519" {
		t.Errorf("path was rewritten to %q", path)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	got, err := expandHome("~/.ssh/id_ed25519")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".ssh/id_ed25519"); got != want {
		t.Errorf("expandHome = %q, want %q", got, want)
	}
	// "~user" is not expanded, matching git.
	if got, _ := expandHome("~other/key"); got != "~other/key" {
		t.Errorf("expected ~other/key to be left alone, got %q", got)
	}
}

func TestParseGitBool(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "yes", "on", "1", " true "} {
		if !parseGitBool(v) {
			t.Errorf("parseGitBool(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "false", "no", "off", "0", "maybe"} {
		if parseGitBool(v) {
			t.Errorf("parseGitBool(%q) = true, want false", v)
		}
	}
}
