package chain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// stubSignature is shaped like a real armored SSH signature (multi-line,
// which is what exercises go-git's gpgsig continuation-line encoding)
// without needing openssh to be installed.
const stubSignature = `-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgc3R1YgAAAANnaXQAAAAAAAAABn
NoYTUxMgAAAFMAAAALc3NoLWVkMjU1MTkAAABAc3R1Yg==
-----END SSH SIGNATURE-----
`

const signingTestEntity = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func stubSigner() Signer {
	return func([]byte) (string, error) { return stubSignature, nil }
}

// TestAppendEventSignatureLeavesTreeUnchanged is the determinism guarantee
// ADR-0014 rests on: attaching a signature changes the commit OID but must
// not change a single byte of the materialized tree
// (docs/02-data-model.md「決定性の不変条件」), so clones that sign and clones
// that do not still converge on identical trees.
func TestAppendEventSignatureLeavesTreeUnchanged(t *testing.T) {
	requireGit(t)
	repo, err := OpenRepository(initBareDir(t))
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(signingTestEntity, signingTestEntity, "issue.create", map[string]any{"title": "t"})
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Unix(1785000000, 0).UTC()}
	files := map[string][]byte{
		"meta.json": []byte("{\n  \"title\": \"t\"\n}\n"),
		"body.md":   []byte("body\n"),
	}

	unsigned, err := AppendEvent(repo, ZeroHash, env, "issue.create: t", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := AppendEvent(repo, ZeroHash, env, "issue.create: t", files, sig, WithSigner(stubSigner()))
	if err != nil {
		t.Fatal(err)
	}

	if unsigned == signed {
		t.Fatal("expected the signature to change the commit OID")
	}
	unsignedCommit, err := repo.CommitObject(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	signedCommit, err := repo.CommitObject(signed)
	if err != nil {
		t.Fatal(err)
	}
	if unsignedCommit.TreeHash != signedCommit.TreeHash {
		t.Fatalf("tree changed with signing: unsigned %s, signed %s", unsignedCommit.TreeHash, signedCommit.TreeHash)
	}
	if unsignedCommit.Message != signedCommit.Message {
		t.Error("commit message changed with signing")
	}
	if got := strings.TrimSpace(signedCommit.PGPSignature); got != strings.TrimSpace(stubSignature) {
		t.Errorf("signature did not round-trip:\n%s", got)
	}
	if unsignedCommit.PGPSignature != "" {
		t.Error("expected no signature when WithSigner is not passed")
	}
}

// TestAppendEventNilSignerIsUnsigned pins the "nil means do not sign"
// contract that lets callers pass sign.ResolveSigner's result straight
// through without branching on whether signing is configured.
func TestAppendEventNilSignerIsUnsigned(t *testing.T) {
	requireGit(t)
	repo, err := OpenRepository(initBareDir(t))
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(signingTestEntity, signingTestEntity, "issue.create", map[string]any{"title": "t"})
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Unix(1785000000, 0).UTC()}

	h, err := AppendEvent(repo, ZeroHash, env, "issue.create: t",
		map[string][]byte{"meta.json": []byte("{}\n")}, sig, WithSigner(nil))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(h)
	if err != nil {
		t.Fatal(err)
	}
	if commit.PGPSignature != "" {
		t.Error("expected a nil signer to leave the commit unsigned")
	}
}

// TestAppendEventSignerErrorAborts checks that a failing signer stops the
// write instead of silently producing an unsigned commit
// (ADR-0014「黙って無署名にしない」).
func TestAppendEventSignerErrorAborts(t *testing.T) {
	requireGit(t)
	repo, err := OpenRepository(initBareDir(t))
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(signingTestEntity, signingTestEntity, "issue.create", map[string]any{"title": "t"})
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Unix(1785000000, 0).UTC()}
	sentinel := errors.New("signing key unavailable")

	_, err = AppendEvent(repo, ZeroHash, env, "issue.create: t",
		map[string][]byte{"meta.json": []byte("{}\n")}, sig,
		WithSigner(func([]byte) (string, error) { return "", sentinel }))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the signer's error to surface, got %v", err)
	}
}

// TestAppendMergeSigns covers the merge path syncapp uses for event-union
// merge commits.
func TestAppendMergeSigns(t *testing.T) {
	requireGit(t)
	repo, err := OpenRepository(initBareDir(t))
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Unix(1785000000, 0).UTC()}
	files := map[string][]byte{"meta.json": []byte("{}\n")}

	a, err := AppendEvent(repo, ZeroHash,
		testEnvelope(signingTestEntity, signingTestEntity, "issue.create", map[string]any{"title": "a"}),
		"issue.create: a", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AppendEvent(repo, ZeroHash,
		testEnvelope("01j8x0a2b3c4d5e6f7g8h9j0kb", signingTestEntity, "issue.create", map[string]any{"title": "b"}),
		"issue.create: b", files, sig)
	if err != nil {
		t.Fatal(err)
	}

	merged, err := AppendMerge(repo, [2]plumbing.Hash{a, b}, files, sig, WithSigner(stubSigner()))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(merged)
	if err != nil {
		t.Fatal(err)
	}
	if commit.PGPSignature == "" {
		t.Error("expected the merge commit to carry a signature")
	}
	if len(commit.ParentHashes) != 2 {
		t.Errorf("expected 2 parents, got %d", len(commit.ParentHashes))
	}
}
