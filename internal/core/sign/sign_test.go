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

func TestHasUnrevokedOrTimelyKey(t *testing.T) {
	entries := []materialize.SignerEntry{
		{Email: "a@example.com", Pub: "k1", RevokedAt: ""},
	}
	if !hasUnrevokedOrTimelyKey(entries, "a@example.com", "2026-07-04T00:00:00.000Z") {
		t.Error("expected an active key to always pass")
	}

	revokedEntries := []materialize.SignerEntry{
		{Email: "a@example.com", Pub: "k1", RevokedAt: "2026-07-04T00:00:00.000Z"},
	}
	if !hasUnrevokedOrTimelyKey(revokedEntries, "a@example.com", "2026-07-01T00:00:00.000Z") {
		t.Error("expected a signature timestamped before revocation to pass")
	}
	if hasUnrevokedOrTimelyKey(revokedEntries, "a@example.com", "2026-07-05T00:00:00.000Z") {
		t.Error("expected a signature timestamped after revocation to fail")
	}
	if hasUnrevokedOrTimelyKey(revokedEntries, "nobody@example.com", "2026-07-01T00:00:00.000Z") {
		t.Error("expected an unknown email to have no keys at all")
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
