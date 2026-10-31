package sign

import (
	"os"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestBuildAllowedSignersDropsCommentIncludesRevoked(t *testing.T) {
	entries := []materialize.SignerEntry{
		{Email: "a@example.com", Pub: "ssh-ed25519 AAAA yuumiya@main"},
		{Email: "a@example.com", Pub: "ssh-ed25519 BBBB old-key", RevokedAt: "2026-07-01T00:00:00.000Z"},
	}
	body := string(BuildAllowedSigners(entries))
	if !strings.Contains(body, "a@example.com ssh-ed25519 AAAA\n") {
		t.Errorf("expected active key line without comment, got:\n%s", body)
	}
	if !strings.Contains(body, "a@example.com ssh-ed25519 BBBB\n") {
		t.Errorf("expected revoked key to still be included (old signatures must verify), got:\n%s", body)
	}
}

// TestBuildAllowedSignersSkipsUnsafeLines: a malformed registry value must
// never become an allowed_signers line that ssh-keygen would read
// differently (a space in the email turns the rest into an options field)
// (#127).
func TestBuildAllowedSignersSkipsUnsafeLines(t *testing.T) {
	entries := []materialize.SignerEntry{
		{Email: "carol smith@example.com", Pub: "ssh-ed25519 AAAA"},
		{Email: "noat.example.com", Pub: "ssh-ed25519 AAAA"},
		{Email: "*@example.com", Pub: "ssh-ed25519 AAAA"},
		{Email: "d@example.com", Pub: "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{Email: "e@example.com", Pub: "ssh-ed25519 not!base64"},
		{Email: "ok@example.com", Pub: "ssh-ed25519 AAAA c"},
	}
	if got, want := string(BuildAllowedSigners(entries)), "ok@example.com ssh-ed25519 AAAA\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFirstTwoFields(t *testing.T) {
	cases := map[string]string{
		"ssh-ed25519 AAAA comment here": "ssh-ed25519 AAAA",
		"ssh-ed25519 AAAA":              "ssh-ed25519 AAAA",
		"ssh-ed25519":                   "",
		"":                              "",
	}
	for input, want := range cases {
		if got := firstTwoFields(input); got != want {
			t.Errorf("firstTwoFields(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParsePrincipal(t *testing.T) {
	output := `Good "git" signature for alice@example.com with ED25519 key SHA256:abc123`
	if got := ParsePrincipal(output); got != "alice@example.com" {
		t.Errorf("got %q", got)
	}
	if got := ParsePrincipal("no match here"); got != "" {
		t.Errorf("expected empty string for unparsable output, got %q", got)
	}
}

func TestSigningKeyValidAt(t *testing.T) {
	const k1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINb/HvyUTIDZYyBZaz6iHIOXxLN881kIXkr6k5mDrDVi laptop"
	const k2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGVoZWhlaGVoZWhlaGVoZWhlaGVoZWhlaGU= desk"
	fp1, fp2 := Fingerprint(k1), Fingerprint(k2)
	entries := []materialize.SignerEntry{
		{Email: "a@example.com", Pub: k1, RevokedAt: "2026-07-04T00:00:00.000Z"},
		{Email: "a@example.com", Pub: k2},
	}
	cases := []struct {
		name, email, fp, ts string
		want                bool
	}{
		{"revoked key before revocation", "a@example.com", fp1, "2026-07-01T00:00:00.000Z", true},
		{"revoked key after revocation, other key active", "a@example.com", fp1, "2026-07-05T00:00:00.000Z", false},
		{"revoked key at revocation instant", "a@example.com", fp1, "2026-07-04T00:00:00.000Z", false},
		{"active key", "a@example.com", fp2, "2026-07-05T00:00:00.000Z", true},
		{"unknown fingerprint", "a@example.com", "SHA256:nope", "2026-07-01T00:00:00.000Z", false},
		{"unknown email", "nobody@example.com", fp2, "2026-07-01T00:00:00.000Z", false},
		{"no fingerprint, before any revocation", "a@example.com", "", "2026-07-01T00:00:00.000Z", true},
		{"no fingerprint, after a revocation: conservative", "a@example.com", "", "2026-07-05T00:00:00.000Z", false},
	}
	for _, c := range cases {
		if got := signingKeyValidAt(entries, c.email, c.fp, c.ts); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	// The same key material registered under two comments, one revoked:
	// the revocation wins.
	dup := []materialize.SignerEntry{
		{Email: "a@example.com", Pub: k1},
		{Email: "a@example.com", Pub: strings.Fields(k1)[0] + " " + strings.Fields(k1)[1] + " other", RevokedAt: "2026-07-04T00:00:00.000Z"},
	}
	if signingKeyValidAt(dup, "a@example.com", fp1, "2026-07-05T00:00:00.000Z") {
		t.Error("a revoked duplicate of the signing key must invalidate it")
	}
}

// TestFingerprintMatchesSSHKeygen: the value ssh-keygen -lf prints for this
// key.
func TestFingerprintMatchesSSHKeygen(t *testing.T) {
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINb/HvyUTIDZYyBZaz6iHIOXxLN881kIXkr6k5mDrDVi laptop"
	if got, want := Fingerprint(pub), "SHA256:i6lKiyT+5JYt8mbDtpGFqLJ+li/zwb+kIZZ14GUCQic"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ParseFingerprint(`Good "git" signature for a@x.com with ED25519 key SHA256:i6lKiyT+5JYt8mbDtpGFqLJ+li/zwb+kIZZ14GUCQic`); got != "SHA256:i6lKiyT+5JYt8mbDtpGFqLJ+li/zwb+kIZZ14GUCQic" {
		t.Fatalf("ParseFingerprint = %q", got)
	}
}

func TestWriteAllowedSignersTempFileRoundTrip(t *testing.T) {
	body := []byte("a@example.com ssh-ed25519 AAAA\n")
	path, cleanup, err := WriteAllowedSignersTempFile(body)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("got %q want %q", got, string(body))
	}
}
