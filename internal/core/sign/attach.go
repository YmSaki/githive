package sign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// ErrUnsupportedFormat means commit.gpgsign is on but gpg.format is not
// "ssh". githive only knows how to attach SSH signatures (ADR-0007), and
// silently writing an unsigned commit when the user asked for a signed one
// is the most dangerous failure mode, so this is an error rather than a
// downgrade (ADR-0014).
var ErrUnsupportedFormat = errors.New("sign: commit signing is enabled but gpg.format is not ssh")

// ErrSigningKeyMissing means commit.gpgsign is on and gpg.format is ssh,
// but user.signingkey is unset, so there is nothing to sign with.
var ErrSigningKeyMissing = errors.New("sign: commit signing is enabled but user.signingkey is not set")

// defaultSSHProgram matches git's default for gpg.ssh.program.
const defaultSSHProgram = "ssh-keygen"

// gitNamespace is the signature namespace git uses for commit signatures
// (`ssh-keygen -Y sign -n git`). Signatures made under a different
// namespace do not verify as commit signatures.
const gitNamespace = "git"

// ResolveSigner reads dir's commit-signing configuration and returns the
// chain.Signer to use, or (nil, cleanup, nil) when signing is off - a nil
// signer is what chain.WithSigner takes to mean "do not sign", so callers
// need no branch. The returned cleanup must always be called; it removes
// any temp file created to hold a literal public key from user.signingkey.
//
// Per ADR-0014 the configuration is resolved here (app layer callers pass
// the result down to core/chain) so that core/chain never reads git config.
// An incomplete configuration is an error, matching git itself: see
// ErrUnsupportedFormat and ErrSigningKeyMissing.
//
// ctx is captured by the returned Signer, which runs a child process per
// commit. The Signer is meant to be used within the same operation that
// resolved it - callers resolve once per write and discard it afterwards.
func ResolveSigner(ctx context.Context, dir string) (chain.Signer, func(), error) {
	noop := func() {}
	r := gitx.New(dir)

	gpgsign, err := r.ConfigGet(ctx, "commit.gpgsign")
	if err != nil {
		return nil, noop, err
	}
	if !parseGitBool(gpgsign) {
		return nil, noop, nil
	}

	format, err := r.ConfigGet(ctx, "gpg.format")
	if err != nil {
		return nil, noop, err
	}
	if format != "ssh" {
		if format == "" {
			format = "openpgp (git's default)"
		}
		return nil, noop, fmt.Errorf("%w: gpg.format is %s; set gpg.format=ssh or turn off commit.gpgsign", ErrUnsupportedFormat, format)
	}

	signingKey, err := r.ConfigGet(ctx, "user.signingkey")
	if err != nil {
		return nil, noop, err
	}
	if signingKey == "" {
		return nil, noop, ErrSigningKeyMissing
	}

	program, err := r.ConfigGet(ctx, "gpg.ssh.program")
	if err != nil {
		return nil, noop, err
	}

	keyPath, cleanup, err := resolveKeyFile(signingKey)
	if err != nil {
		return nil, noop, err
	}
	return func(payload []byte) (string, error) {
		return SignBuffer(ctx, program, keyPath, payload)
	}, cleanup, nil
}

// SignBuffer produces an armored SSH signature over payload by running
// `<program> -Y sign -n git -f <keyPath> <tempfile>` and reading the
// `<tempfile>.sig` that openssh writes next to it (ADR-0014). program may
// be "" to use ssh-keygen, git's default gpg.ssh.program.
//
// The payload is handed over as a file rather than on stdin because a
// gpg.ssh.program replacement is not guaranteed to accept `-` (git itself
// always passes a file).
func SignBuffer(ctx context.Context, program, keyPath string, payload []byte) (string, error) {
	if program == "" {
		program = defaultSSHProgram
	}

	f, err := os.CreateTemp("", "githive-sign-payload-*")
	if err != nil {
		return "", fmt.Errorf("sign: create temp payload file: %w", err)
	}
	payloadPath := f.Name()
	sigPath := payloadPath + ".sig"
	defer func() {
		os.Remove(payloadPath)
		os.Remove(sigPath)
	}()
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return "", fmt.Errorf("sign: write temp payload file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("sign: close temp payload file: %w", err)
	}

	cmd := exec.CommandContext(ctx, program, "-Y", "sign", "-n", gitNamespace, "-f", keyPath, payloadPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sign: %s -Y sign: %w: %s", program, err, strings.TrimSpace(stderr.String()))
	}

	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return "", fmt.Errorf("sign: read signature written by %s: %w", program, err)
	}
	if len(sig) == 0 {
		return "", fmt.Errorf("sign: %s produced an empty signature", program)
	}
	return string(sig), nil
}

// resolveKeyFile turns a user.signingkey value into a path that can be
// passed to `ssh-keygen -f`. git accepts three spellings, and so do we:
//
//   - a path to a key file, possibly starting with "~";
//   - a literal public key ("ssh-ed25519 AAAA...", "sk-ssh-..."), which
//     openssh can sign with as long as an agent holds the private half;
//   - either of the above prefixed with "key::".
//
// A literal key is written to a temp file, since -f wants a path. The
// returned cleanup removes it and is a no-op for the path spelling.
func resolveKeyFile(signingKey string) (path string, cleanup func(), err error) {
	noop := func() {}
	value := strings.TrimPrefix(signingKey, "key::")

	if isLiteralPublicKey(value) {
		f, err := os.CreateTemp("", "githive-signing-key-*.pub")
		if err != nil {
			return "", noop, fmt.Errorf("sign: create temp signing key file: %w", err)
		}
		name := f.Name()
		remove := func() { os.Remove(name) }
		if _, err := f.WriteString(strings.TrimRight(value, "\n") + "\n"); err != nil {
			f.Close()
			remove()
			return "", noop, fmt.Errorf("sign: write temp signing key file: %w", err)
		}
		if err := f.Close(); err != nil {
			remove()
			return "", noop, fmt.Errorf("sign: close temp signing key file: %w", err)
		}
		return name, remove, nil
	}

	expanded, err := expandHome(value)
	if err != nil {
		return "", noop, err
	}
	return expanded, noop, nil
}

// literalKeyPrefixes are the public key type names openssh emits, i.e. the
// shapes a user.signingkey value takes when it holds the key itself rather
// than a path to it.
var literalKeyPrefixes = []string{"ssh-", "ecdsa-", "sk-ssh-", "sk-ecdsa-"}

func isLiteralPublicKey(s string) bool {
	for _, p := range literalKeyPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// expandHome resolves a leading "~" (alone or as "~/...") against the
// current user's home directory. Other "~user" forms are left as-is; git
// does not expand them either.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sign: expand ~ in user.signingkey: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// parseGitBool interprets a git config boolean the way git does for
// `--type=bool`: the listed spellings are true, everything else (including
// an unset key, which ConfigGet reports as "") is false.
func parseGitBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	default:
		return false
	}
}
