package usersapp

import (
	"errors"
	"fmt"
	"strings"
)

// Input validation for the users registry (docs/11-security.md「実装上の
// 注意」: 入力は白リストで検証する). Anything written here is replicated to
// every clone and can never be deleted (docs/11-security.md「秘匿情報」), so
// a malformed value has to be rejected before it reaches an event.
var (
	ErrInvalidKey   = errors.New("usersapp: invalid SSH public key")
	ErrPrivateKey   = errors.New("usersapp: refusing to register a private key")
	ErrInvalidEmail = errors.New("usersapp: invalid email")
	ErrKeyExists    = errors.New("usersapp: key already registered")
	ErrKeyNotFound  = errors.New("usersapp: key not found")
)

// sshPublicKeyTypes are the key-type fields OpenSSH's public key line
// format (ssh-keygen(1)) uses in practice.
var sshPublicKeyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true, "ssh-dss": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
	"sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// ValidatePublicKey checks that pub has the shape "<type> <base64>
// [comment]" with a recognized key type, and rejects private key material
// with a dedicated error. It is a cheap shape check, not full parsing: the
// real cryptographic validation happens in git verify-commit when a
// signature is checked.
func ValidatePublicKey(pub string) error {
	if strings.Contains(pub, "-----BEGIN") || strings.Contains(pub, "PRIVATE KEY") {
		return fmt.Errorf("%w: input looks like a private key; pass the .pub file instead", ErrPrivateKey)
	}
	if strings.ContainsAny(pub, "\r\n") {
		return fmt.Errorf("%w: must be a single line", ErrInvalidKey)
	}
	fields := strings.Fields(pub)
	if len(fields) < 2 || !sshPublicKeyTypes[fields[0]] {
		return fmt.Errorf("%w: want \"<type> <base64-data> [comment]\", got %q", ErrInvalidKey, pub)
	}
	data := fields[1]
	if len(data) < 20 {
		return fmt.Errorf("%w: key data too short", ErrInvalidKey)
	}
	for _, r := range data {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
		default:
			return fmt.Errorf("%w: key data is not base64", ErrInvalidKey)
		}
	}
	return nil
}

// KeyMaterial returns the "<type> <base64>" identity of a public key line,
// dropping any trailing comment. Two registry entries with the same
// material are the same key regardless of comment (the same identity
// core/sign uses when building allowed_signers).
func KeyMaterial(pub string) string {
	fields := strings.Fields(pub)
	if len(fields) < 2 {
		return strings.TrimSpace(pub)
	}
	return fields[0] + " " + fields[1]
}

// ValidEmail applies docs/02-data-model.md's actor rule (空白を含まない、
// `@` を含む、320 byte 以下) to a registry email, plus rejects the
// characters ssh-keygen's allowed_signers principal patterns treat
// specially (`,` `*` `?` `!` `"`), since every registered email is written
// into that file verbatim.
func ValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 320 || !strings.Contains(email, "@") {
		return false
	}
	for _, r := range email {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`,*?!"`, r) {
			return false
		}
	}
	return !strings.ContainsAny(email, "\u0085   ")
}
