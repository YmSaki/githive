// Package sign implements SSH commit-signature verification against the
// users registry (docs/11-security.md「SSH 署名」, ADR-0007). All
// cryptographic work is delegated to system git/ssh
// (`git verify-commit`) - this package only builds the allowed_signers
// input and interprets the result; docs/11-security.md「実装上の注意」:
// 暗号処理を自前実装しない.
package sign

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// BuildAllowedSigners renders the registry state's signers into an
// allowed_signers file body (one line per (email, key) pair). Revoked keys
// are included (docs/11-security.md「鍵のローテーション」: 過去の署名は
// 有効なまま) - see materialize.AllowedSigners for why they aren't
// dropped. The revocation timestamp itself is not encoded in this file;
// VerifyCommit checks it per signing key after establishing cryptographic
// validity, since this package cannot assume ssh-keygen's optional
// valid-before support is present/consistent across git/ssh versions.
func BuildAllowedSigners(entries []materialize.SignerEntry) []byte {
	var b strings.Builder
	for _, e := range entries {
		keyField := firstTwoFields(e.Pub)
		if keyField == "" || !safePrincipal(e.Email) || !safeKeyField(keyField) {
			// 台帳に不正な値が入っていても allowed_signers の行として解釈
			// させない（空白入りの email は options 欄として読まれる）。
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", e.Email, keyField)
	}
	return []byte(b.String())
}

// firstTwoFields extracts "<keytype> <base64key>" from a pub key string
// that may carry a trailing comment (as stored, matching the
// authorized_keys-style format in docs/features/users.md's example:
// "ssh-ed25519 AAAA... yuumiya@main"). allowed_signers wants exactly
// "<keytype> <base64key>" after the principal, so any comment is dropped.
func firstTwoFields(pub string) string {
	fields := strings.Fields(pub)
	if len(fields) < 2 {
		return ""
	}
	return fields[0] + " " + fields[1]
}

// safePrincipal reports whether email can be written as an allowed_signers
// principal without being reinterpreted: no whitespace or control
// characters (which would split it into an options field), none of the
// principal-pattern metacharacters `,*?!"`, and an '@' (docs/02 actor 規則).
func safePrincipal(email string) bool {
	if !strings.Contains(email, "@") {
		return false
	}
	for _, r := range email {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`,*?!"`, r) {
			return false
		}
	}
	return true
}

// safeKeyField reports whether "<type> <base64>" has a plain type token
// and base64-only key data.
func safeKeyField(keyField string) bool {
	typ, data, _ := strings.Cut(keyField, " ")
	if typ == "" || data == "" || strings.HasPrefix(typ, "-") {
		return false
	}
	for _, r := range data {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
		default:
			return false
		}
	}
	return true
}

// WriteAllowedSignersTempFile writes body to a new temp file and returns
// its path. Callers must remove it when done
// (docs/11-security.md「実装上の注意」: 一時ファイルとして都度生成し、
// 検証後に削除する).
func WriteAllowedSignersTempFile(body []byte) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "githive-allowed-signers-*")
	if err != nil {
		return "", nil, fmt.Errorf("sign: create temp allowed_signers file: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("sign: write temp allowed_signers file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("sign: close temp allowed_signers file: %w", err)
	}
	name := f.Name()
	return name, func() { os.Remove(name) }, nil
}

// Result is one commit's verification outcome.
type Result struct {
	Valid     bool
	Principal string // the email git matched the signature to, if parseable
	Output    string
}

var principalRe = regexp.MustCompile(`signature for ([^\s]+)`)

// ParsePrincipal extracts the matched principal (email) from
// `git verify-commit --raw`'s "Good/Bad ... signature for <email> ..."
// output, or "" if it can't be found (older git/ssh versions may format
// this differently; callers should treat that as "can't check per-key
// revocation timing" rather than as an error, per docs/11-security.md
// 「実装上の注意」).
func ParsePrincipal(output string) string {
	m := principalRe.FindStringSubmatch(output)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// VerifyCommit checks one commit's SSH signature against the given
// registry state (docs/11-security.md「検証規則」1-3):
//  1. the signature must be cryptographically valid against some
//     registered key (any key ever registered, active or revoked, is
//     accepted for this step - see BuildAllowedSigners);
//  2. the matched principal (email) must be commitEmail;
//  3. the signing key itself must not have been revoked at or before
//     commitTime (「署名時刻が鍵の revoked_at より前であること」). The key is
//     identified by the "SHA256:<fp>" fingerprint git prints for SSH
//     signatures ("Good "git" signature for <email> with ED25519 key
//     SHA256:..."), matched against each registered key's fingerprint
//     (unpadded base64 of SHA-256 over the decoded key blob - a hash, not a
//     signature check, so 暗号処理を自前実装しない still holds). If the
//     fingerprint cannot be parsed from git's output, the check falls back
//     to the conservative rule that every key registered for commitEmail
//     must be valid at commitTime.
func VerifyCommit(ctx context.Context, dir string, commitHash string, state *materialize.State, commitEmail, commitTime string) (Result, error) {
	if owners := materialize.EmailOwners(state)[commitEmail]; len(owners) > 1 {
		return Result{Valid: false, Output: fmt.Sprintf("ambiguous email %q belongs to users %s", commitEmail, strings.Join(owners, ", "))}, nil
	}
	entries := materialize.AllowedSigners(state)
	// A key may legitimately belong to several aliases of one user. Ask Git
	// to verify only the requested principal rather than whichever alias it
	// encounters first in allowed_signers (#111).
	matched := entries[:0]
	for _, entry := range entries {
		if entry.Email == commitEmail {
			matched = append(matched, entry)
		}
	}
	entries = matched
	body := BuildAllowedSigners(entries)
	path, cleanup, err := WriteAllowedSignersTempFile(body)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	r := gitx.New(dir)
	raw, err := r.VerifyCommit(ctx, commitHash, path)
	if err != nil {
		return Result{}, err
	}
	if !raw.Valid {
		return Result{Valid: false, Output: raw.Output}, nil
	}

	principal := ParsePrincipal(raw.Output)
	if principal != "" && principal != commitEmail {
		return Result{Valid: false, Principal: principal, Output: raw.Output}, nil
	}

	if !signingKeyValidAt(entries, commitEmail, ParseFingerprint(raw.Output), commitTime) {
		return Result{Valid: false, Principal: commitEmail, Output: raw.Output}, nil
	}
	return Result{Valid: true, Principal: commitEmail, Output: raw.Output}, nil
}

var fingerprintRe = regexp.MustCompile(`key (SHA256:[A-Za-z0-9+/]+)`)

// ParseFingerprint extracts the "SHA256:<base64>" fingerprint of the
// signing key from `git verify-commit --raw`'s SSH output, or "" if absent.
func ParseFingerprint(output string) string {
	m := fingerprintRe.FindStringSubmatch(output)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// Fingerprint returns OpenSSH's "SHA256:<unpadded base64>" fingerprint of
// a public key line ("<type> <base64> [comment]"), or "" if the key data
// is not valid base64.
func Fingerprint(pub string) string {
	fields := strings.Fields(pub)
	if len(fields) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// signingKeyValidAt applies 検証規則 3 per key. Every registry entry for
// email whose fingerprint equals fp is the signing key (the same key
// material may appear under several comments); it is valid only if none of
// those entries was revoked at or before commitTime. With no fingerprint,
// every key of email must be valid at commitTime.
func signingKeyValidAt(entries []materialize.SignerEntry, email, fp, commitTime string) bool {
	found := false
	for _, e := range entries {
		if e.Email != email {
			continue
		}
		if fp != "" && Fingerprint(e.Pub) != fp {
			continue
		}
		found = true
		if e.RevokedAt != "" && commitTime >= e.RevokedAt {
			return false
		}
	}
	return found
}
