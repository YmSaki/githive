package usersapp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// testPub is a real ed25519 public key (no comment); never used to sign.
const testPub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINb/HvyUTIDZYyBZaz6iHIOXxLN881kIXkr6k5mDrDVi"

func TestValidatePublicKey(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{testPub, nil},
		{testPub + " a@laptop", nil},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----", ErrPrivateKey},
		{"/home/a/.ssh/nosuchfile.pub", ErrInvalidKey},
		{"garbage not a key", ErrInvalidKey},
		{"ssh-ed25519 AAAA", ErrInvalidKey},
		{"ssh-ed25519 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ErrInvalidKey},
		{"ssh-rsa " + testPub[12:], ErrInvalidKey},
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI!!/HvyUTIDZYyBZaz6iHIOXxLN881k", ErrInvalidKey},
		{testPub + "\nssh-ed25519 " + testPub[12:], ErrInvalidKey},
	}
	for _, c := range cases {
		err := ValidatePublicKey(c.in)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("ValidatePublicKey(%q) = %v, want %v", c.in, err, c.want)
		}
	}
}

func TestMalformedKeyErrorDoesNotEchoUnrecognizedMaterial(t *testing.T) {
	const supplied = "unrecognized-private-material-without-a-pem-header"
	err := ValidatePublicKey(supplied)
	if !errors.Is(err, ErrInvalidKey) || strings.Contains(err.Error(), supplied) {
		t.Fatalf("invalid key material must be rejected without echoing it: %v", err)
	}
}

func TestValidEmail(t *testing.T) {
	for in, want := range map[string]bool{
		"a@example.com":     true,
		"carol smith":       false,
		"carol smith@x.com": false,
		"noat":              false,
		"a,b@example.com":   false,
		"*@example.com":     false,
		"a\t@example.com":   false,
		"":                  false,
	} {
		if got := ValidEmail(in); got != want {
			t.Errorf("ValidEmail(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAddUserRejectsInvalidEmail(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	if err := svc.AddUser(context.Background(), "carol", "", "carol smith", "human", nil); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("want ErrInvalidEmail, got %v", err)
	}
}

func TestKeyAddRejectsPrivateKeyAndGarbage(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "", "a@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyAdd(ctx, "alice", "-----BEGIN OPENSSH PRIVATE KEY-----\nxxx\n-----END OPENSSH PRIVATE KEY-----"); !errors.Is(err, ErrPrivateKey) {
		t.Errorf("private key: want ErrPrivateKey, got %v", err)
	}
	if err := svc.KeyAdd(ctx, "alice", "garbage not a key"); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("garbage: want ErrInvalidKey, got %v", err)
	}
	user, _ := svc.GetUser(ctx, "alice")
	if keys := userKeys(user); len(keys) != 0 {
		t.Fatalf("nothing should have been registered, got %v", keys)
	}
}

// TestKeyRevokeMatchesIgnoringComment is #89's reproduction: a key added
// with a comment and revoked without one (or with a different one) must
// actually end up revoked.
func TestKeyRevokeMatchesIgnoringComment(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "", "a@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyAdd(ctx, "alice", testPub+" a@laptop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyRevoke(ctx, "alice", testPub); err != nil {
		t.Fatal(err)
	}
	user, _ := svc.GetUser(ctx, "alice")
	keys := userKeys(user)
	if len(keys) != 1 || keys[0]["revoked_at"] == nil {
		t.Fatalf("key should be revoked, got %v", keys)
	}
	// Revoking again: nothing active matches any more.
	if err := svc.KeyRevoke(ctx, "alice", testPub+" other"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second revoke: want ErrKeyNotFound, got %v", err)
	}
}

// TestKeyAddSameMaterialDifferentComment: the same key material cannot be
// registered twice under different comments (which would let one copy
// survive a revoke), and a revoked key cannot be resurrected by re-adding.
func TestKeyAddSameMaterialDifferentComment(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "", "a@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyAdd(ctx, "alice", testPub+" one"); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyAdd(ctx, "alice", testPub+" two"); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("want ErrKeyExists, got %v", err)
	}
	if err := svc.KeyRevoke(ctx, "alice", testPub); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyAdd(ctx, "alice", testPub+" three"); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("re-adding a revoked key: want ErrKeyExists, got %v", err)
	}
}

func TestKeyRevokeUnknownKeyFails(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "", "a@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.KeyRevoke(ctx, "alice", testPub); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
}
