// Package sign implements SSH commit-signature verification against the
// users registry (docs/11-security.md「SSH 署名」, ADR-0007). All
// cryptographic work is delegated to system git/ssh
// (`git verify-commit`) - this package only builds the allowed_signers
// input and interprets the result; docs/11-security.md「実装上の注意」:
// 暗号処理を自前実装しない.
package sign

import (
	"context"
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
// VerifyCommit's caller (Verify in this package) checks it separately
// after establishing cryptographic validity, since this package cannot
// assume ssh-keygen's optional valid-before support is present/consistent
// across git/ssh versions.
func BuildAllowedSigners(entries []materialize.SignerEntry) []byte {
	var b strings.Builder
	for _, e := range entries {
		keyField := firstTwoFields(e.Pub)
		if keyField == "" {
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
//  2. the matched principal (email) must actually be one of commitEmail's
//     registered emails (checked by the caller comparing to the commit's
//     own committer email, docs/11-security.md 検証規則 2 already requires
//     actor==committer email, enforced at write time);
//  3. if every one of that email's keys is revoked as of commitTime, the
//     signature is rejected even though step 1 succeeded (approximates
//     "signature time must be before the specific key's revocation" at the
//     granularity of "this email has at least one key that was valid at
//     commitTime", since git's verify-commit --raw output does not expose
//     enough to match back to one specific registered key entry).
func VerifyCommit(ctx context.Context, dir string, commitHash string, state *materialize.State, commitEmail, commitTime string) (Result, error) {
	entries := materialize.AllowedSigners(state)
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

	if !hasUnrevokedOrTimelyKey(entries, commitEmail, commitTime) {
		return Result{Valid: false, Principal: commitEmail, Output: raw.Output}, nil
	}
	return Result{Valid: true, Principal: commitEmail, Output: raw.Output}, nil
}

// hasUnrevokedOrTimelyKey reports whether email has at least one key that
// either is still active, or was revoked strictly after commitTime
// (docs/11-security.md: 過去の署名は有効なまま).
func hasUnrevokedOrTimelyKey(entries []materialize.SignerEntry, email, commitTime string) bool {
	for _, e := range entries {
		if e.Email != email {
			continue
		}
		if e.RevokedAt == "" || commitTime < e.RevokedAt {
			return true
		}
	}
	return false
}
