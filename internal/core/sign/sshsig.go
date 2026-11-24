package sign

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/ymsaki/githive/internal/core/materialize"
)

const (
	sshSigHeader = "-----BEGIN SSH SIGNATURE-----"
	sshSigFooter = "-----END SSH SIGNATURE-----"
	sshSigMagic  = "SSHSIG"
)

type sshSigBlob struct {
	Version       uint32
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

type sshSigSignedData struct {
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Hash          []byte
}

func parseSSHSig(armored string) (*sshSigBlob, error) {
	start := strings.Index(armored, sshSigHeader)
	if start < 0 {
		return nil, errors.New("sign: missing SSH SIGNATURE header")
	}
	rest := armored[start+len(sshSigHeader):]
	end := strings.Index(rest, sshSigFooter)
	if end < 0 {
		return nil, errors.New("sign: missing SSH SIGNATURE footer")
	}
	b64 := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, rest[:end])
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("sign: decode sshsig base64: %w", err)
	}
	if len(raw) < len(sshSigMagic) || string(raw[:len(sshSigMagic)]) != sshSigMagic {
		return nil, errors.New("sign: invalid sshsig magic")
	}
	var blob sshSigBlob
	if err := ssh.Unmarshal(raw[len(sshSigMagic):], &blob); err != nil {
		return nil, fmt.Errorf("sign: unmarshal sshsig: %w", err)
	}
	if blob.Version != 1 {
		return nil, fmt.Errorf("sign: unsupported sshsig version %d", blob.Version)
	}
	return &blob, nil
}

func verifySSHSigPayload(armored string, payload []byte, expectedNamespace string) (ssh.PublicKey, string, error) {
	blob, err := parseSSHSig(armored)
	if err != nil {
		return nil, "", err
	}
	if blob.Namespace != expectedNamespace {
		return nil, "", fmt.Errorf("sign: sshsig namespace %q != %q", blob.Namespace, expectedNamespace)
	}
	var msgHash []byte
	switch blob.HashAlgorithm {
	case "sha256":
		sum := sha256.Sum256(payload)
		msgHash = sum[:]
	case "sha512":
		sum := sha512.Sum512(payload)
		msgHash = sum[:]
	default:
		return nil, "", fmt.Errorf("sign: unsupported hash algorithm %q", blob.HashAlgorithm)
	}

	pubKey, err := ssh.ParsePublicKey(blob.PublicKey)
	if err != nil {
		return nil, "", fmt.Errorf("sign: parse public key: %w", err)
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(blob.Signature, &sig); err != nil {
		return nil, "", fmt.Errorf("sign: unmarshal signature: %w", err)
	}

	signed := append([]byte(sshSigMagic), ssh.Marshal(sshSigSignedData{
		Namespace:     blob.Namespace,
		Reserved:      blob.Reserved,
		HashAlgorithm: blob.HashAlgorithm,
		Hash:          msgHash,
	})...)
	if err := pubKey.Verify(signed, &sig); err != nil {
		return nil, "", fmt.Errorf("sign: verify signature: %w", err)
	}
	sum := sha256.Sum256(pubKey.Marshal())
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	return pubKey, fp, nil
}

func commitPayload(commit *object.Commit) ([]byte, error) {
	var mem plumbing.MemoryObject
	if err := commit.EncodeWithoutSignature(&mem); err != nil {
		return nil, fmt.Errorf("sign: encode commit without signature: %w", err)
	}
	r, err := mem.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type indexedSigner struct {
	fp        string
	revokedAt string
}

// Verifier caches registry signer policy and verifies commit SSH signatures
// in-process using golang.org/x/crypto/ssh without spawning git/ssh-keygen.
type Verifier struct {
	emailOwners    map[string][]string
	entriesByEmail map[string][]indexedSigner
}

// NewVerifier builds a Verifier from state, precomputing EmailOwners and
// per-email signer fingerprints once.
func NewVerifier(state *materialize.State) *Verifier {
	owners := materialize.EmailOwners(state)
	entries := materialize.AllowedSigners(state)
	byEmail := make(map[string][]indexedSigner, len(entries))
	for _, e := range entries {
		keyField := firstTwoFields(e.Pub)
		if keyField == "" || !safePrincipal(e.Email) || !safeKeyField(keyField) {
			continue
		}
		fp := Fingerprint(e.Pub)
		if fp == "" {
			continue
		}
		byEmail[e.Email] = append(byEmail[e.Email], indexedSigner{
			fp:        fp,
			revokedAt: e.RevokedAt,
		})
	}
	return &Verifier{
		emailOwners:    owners,
		entriesByEmail: byEmail,
	}
}

// VerifyCommitCrypto checks only the commit's cryptographic SSH signature
// against the "git" namespace, independent of any users registry state.
func VerifyCommitCrypto(commit *object.Commit) (sigValid bool, pubKeyFP, keyType string) {
	if strings.TrimSpace(commit.PGPSignature) == "" {
		return false, "", ""
	}
	payload, err := commitPayload(commit)
	if err != nil {
		return false, "", ""
	}
	pubKey, fp, err := verifySSHSigPayload(commit.PGPSignature, payload, "git")
	if err != nil {
		return false, "", ""
	}
	return true, fp, pubKey.Type()
}

// CheckPolicy evaluates the registry policy (ambiguous email, key ownership,
// and revocation timestamp) for a pre-verified cryptographic signature.
func (v *Verifier) CheckPolicy(sigValid bool, pubKeyFP, keyType, commitEmail, commitTime string) Result {
	if owners := v.emailOwners[commitEmail]; len(owners) > 1 {
		return Result{Valid: false, Output: fmt.Sprintf("ambiguous email %q belongs to users %s", commitEmail, strings.Join(owners, ", "))}
	}
	if !sigValid || pubKeyFP == "" {
		return Result{Valid: false, Output: "invalid or missing signature"}
	}
	signers := v.entriesByEmail[commitEmail]
	found := false
	for _, s := range signers {
		if s.fp != pubKeyFP {
			continue
		}
		found = true
		if s.revokedAt != "" && commitTime >= s.revokedAt {
			return Result{Valid: false, Principal: commitEmail, Output: fmt.Sprintf("key %s revoked at %s", pubKeyFP, s.revokedAt)}
		}
	}
	if !found {
		return Result{Valid: false, Output: fmt.Sprintf("key %s not registered for %s", pubKeyFP, commitEmail)}
	}
	return Result{
		Valid:     true,
		Principal: commitEmail,
		Output:    fmt.Sprintf("Good \"git\" signature for %s with %s key %s", commitEmail, keyType, pubKeyFP),
	}
}

// VerifyCommitObject verifies commit's SSH signature in-process against the
// precomputed registry policy (docs/11-security.md「検証規則」1-3).
func (v *Verifier) VerifyCommitObject(commit *object.Commit, commitEmail, commitTime string) Result {
	sigValid, fp, kt := VerifyCommitCrypto(commit)
	return v.CheckPolicy(sigValid, fp, kt, commitEmail, commitTime)
}
