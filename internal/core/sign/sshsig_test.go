package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/ymsaki/githive/internal/core/materialize"
)

func genEd25519Key(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	return signer, pubLine
}

func signSSHSig(t *testing.T, signer ssh.Signer, namespace string, payload []byte) string {
	t.Helper()
	sum := sha256.Sum256(payload)
	signed := append([]byte(sshSigMagic), ssh.Marshal(sshSigSignedData{
		Namespace:     namespace,
		Reserved:      "",
		HashAlgorithm: "sha256",
		Hash:          sum[:],
	})...)
	sig, err := signer.Sign(rand.Reader, signed)
	if err != nil {
		t.Fatal(err)
	}
	blob := append([]byte(sshSigMagic), ssh.Marshal(sshSigBlob{
		Version:       1,
		PublicKey:     signer.PublicKey().Marshal(),
		Namespace:     namespace,
		Reserved:      "",
		HashAlgorithm: "sha256",
		Signature:     ssh.Marshal(sig),
	})...)
	b64 := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString(sshSigHeader + "\n")
	for i := 0; i < len(b64); i += 70 {
		end := i + 70
		if end > len(b64) {
			end = len(b64)
		}
		b.WriteString(b64[i:end] + "\n")
	}
	b.WriteString(sshSigFooter)
	return b.String()
}

func makeSignedCommit(t *testing.T, repo *git.Repository, signer ssh.Signer, namespace, email, msg string) *object.Commit {
	t.Helper()
	sig := object.Signature{Name: "test", Email: email, When: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	commit := &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   msg,
		TreeHash:  plumbing.ZeroHash,
	}
	if signer != nil {
		payload, err := commitPayload(commit)
		if err != nil {
			t.Fatal(err)
		}
		commit.PGPSignature = signSSHSig(t, signer, namespace, payload)
	}
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		t.Fatal(err)
	}
	h, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	out, err := object.GetCommit(repo.Storer, h)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifierInProcessSSHSig(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	signerA, pubA := genEd25519Key(t)
	signerB, pubB := genEd25519Key(t)

	state := &materialize.State{
		Collections: map[string]map[string]any{
			"users": {
				"alice": map[string]any{
					"emails": []any{"alice@example.com", "alice+alias@example.com"},
					"keys": []any{
						map[string]any{"pub": pubA},
						map[string]any{"pub": pubB, "revoked_at": "2026-01-01T12:00:00.000Z"},
					},
				},
				"bob": map[string]any{
					"emails": []any{"bob@example.com"},
					"keys":   []any{map[string]any{"pub": pubB}},
				},
			},
		},
	}

	v := NewVerifier(state)

	cases := []struct {
		name      string
		commit    *object.Commit
		email     string
		ts        string
		wantValid bool
	}{
		{
			name:      "valid_signature",
			commit:    makeSignedCommit(t, repo, signerA, "git", "alice@example.com", "valid"),
			email:     "alice@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: true,
		},
		{
			name:      "valid_alias_email",
			commit:    makeSignedCommit(t, repo, signerA, "git", "alice+alias@example.com", "alias"),
			email:     "alice+alias@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: true,
		},
		{
			name:      "unsigned_commit",
			commit:    makeSignedCommit(t, repo, nil, "git", "alice@example.com", "unsigned"),
			email:     "alice@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: false,
		},
		{
			name:      "wrong_key_for_email",
			commit:    makeSignedCommit(t, repo, signerA, "git", "bob@example.com", "wrong key"),
			email:     "bob@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: false,
		},
		{
			name:      "wrong_namespace",
			commit:    makeSignedCommit(t, repo, signerA, "file", "alice@example.com", "wrong ns"),
			email:     "alice@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: false,
		},
		{
			name:      "revoked_key_before_revocation",
			commit:    makeSignedCommit(t, repo, signerB, "git", "alice@example.com", "before revoke"),
			email:     "alice@example.com",
			ts:        "2026-01-01T11:59:59.999Z",
			wantValid: true,
		},
		{
			name:      "revoked_key_at_revocation",
			commit:    makeSignedCommit(t, repo, signerB, "git", "alice@example.com", "at revoke"),
			email:     "alice@example.com",
			ts:        "2026-01-01T12:00:00.000Z",
			wantValid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := v.VerifyCommitObject(tc.commit, tc.email, tc.ts)
			if res.Valid != tc.wantValid {
				t.Fatalf("got Valid=%v (output=%q), want %v", res.Valid, res.Output, tc.wantValid)
			}
		})
	}

	t.Run("tampered_message_fails", func(t *testing.T) {
		c := makeSignedCommit(t, repo, signerA, "git", "alice@example.com", "original")
		c.Message = "tampered"
		res := v.VerifyCommitObject(c, "alice@example.com", "2026-01-01T12:00:00.000Z")
		if res.Valid {
			t.Fatalf("expected tampered commit to fail verification")
		}
	})

	t.Run("rsa_disallows_legacy_ssh_rsa", func(t *testing.T) {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		rsaSigner, err := ssh.NewSignerFromKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		algSigner, ok := rsaSigner.(ssh.AlgorithmSigner)
		if !ok {
			t.Fatal("rsa signer does not implement AlgorithmSigner")
		}
		rsaPub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(rsaSigner.PublicKey())))
		rsaState := &materialize.State{
			Collections: map[string]map[string]any{
				"users": {
					"carol": map[string]any{
						"emails": []any{"carol@example.com"},
						"keys":   []any{map[string]any{"pub": rsaPub}},
					},
				},
			},
		}
		rv := NewVerifier(rsaState)

		signWithAlg := func(alg string, payload []byte) string {
			sum := sha256.Sum256(payload)
			signed := append([]byte(sshSigMagic), ssh.Marshal(sshSigSignedData{
				Namespace:     "git",
				Reserved:      "",
				HashAlgorithm: "sha256",
				Hash:          sum[:],
			})...)
			sig, err := algSigner.SignWithAlgorithm(rand.Reader, signed, alg)
			if err != nil {
				t.Fatal(err)
			}
			blob := append([]byte(sshSigMagic), ssh.Marshal(sshSigBlob{
				Version:       1,
				PublicKey:     rsaSigner.PublicKey().Marshal(),
				Namespace:     "git",
				Reserved:      "",
				HashAlgorithm: "sha256",
				Signature:     ssh.Marshal(sig),
			})...)
			return sshSigHeader + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n" + sshSigFooter
		}

		sig := object.Signature{Name: "carol", Email: "carol@example.com", When: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
		cLegacy := &object.Commit{Author: sig, Committer: sig, Message: "legacy", TreeHash: plumbing.ZeroHash}
		pLegacy, err := commitPayload(cLegacy)
		if err != nil {
			t.Fatal(err)
		}
		cLegacy.PGPSignature = signWithAlg(ssh.KeyAlgoRSA, pLegacy)
		if res := rv.VerifyCommitObject(cLegacy, "carol@example.com", "2026-01-01T12:00:00.000Z"); res.Valid {
			t.Fatalf("expected legacy ssh-rsa signature to be rejected")
		}

		cSHA256 := &object.Commit{Author: sig, Committer: sig, Message: "sha256", TreeHash: plumbing.ZeroHash}
		pSHA256, err := commitPayload(cSHA256)
		if err != nil {
			t.Fatal(err)
		}
		cSHA256.PGPSignature = signWithAlg(ssh.KeyAlgoRSASHA256, pSHA256)
		if res := rv.VerifyCommitObject(cSHA256, "carol@example.com", "2026-01-01T12:00:00.000Z"); !res.Valid {
			t.Fatalf("expected rsa-sha2-256 signature to succeed: %s", res.Output)
		}
	})
}
