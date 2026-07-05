package usersapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// AgentSetup is the result of AddAgent: everything the caller needs to
// finish wiring up the Agent's git identity
// (docs/features/users.md「Agent のセットアップ」).
type AgentSetup struct {
	Username   string
	Email      string
	PrivateKey string // path to the generated private key
	PublicKey  string // path to the generated .pub file
	PubLine    string // the public key's authorized_keys-format line
	// ConfigSnippet is ready to paste into the Agent's git config (or run
	// as shell commands) to set user.name/user.email/user.signingkey/
	// commit.gpgsign/gpg.format.
	ConfigSnippet string
}

// AddAgent implements `githive users add <name> --agent`
// (docs/features/users.md「Agent のセットアップ」): it mints a
// `.invalid`-domain identity (RFC 6761 guarantees no real mailbox can
// exist there), generates a dedicated ed25519 SSH keypair, registers the
// user and public key in the registry, and returns a config snippet.
func AddAgent(ctx context.Context, dir, username, project, email, keyDir string) (*AgentSetup, error) {
	if !ValidUsername(username) {
		return nil, fmt.Errorf("%w: username %q", ErrInvalidName, username)
	}
	if email == "" {
		if project == "" {
			project = ProjectName(ctx, dir)
		}
		email = fmt.Sprintf("%s@agents.%s.invalid", username, project)
	}
	if keyDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		keyDir = filepath.Join(home, ".ssh")
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("usersapp: create key dir: %w", err)
	}

	keyPath := filepath.Join(keyDir, "githive-"+username)
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return nil, fmt.Errorf("usersapp: ssh-keygen not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen",
		"-t", "ed25519",
		"-N", "",
		"-C", email,
		"-f", keyPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("usersapp: ssh-keygen: %w: %s", err, out)
	}

	pubBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("usersapp: read generated public key: %w", err)
	}
	pubLine := trimNewline(string(pubBytes))

	svc := New(dir)
	if err := svc.AddUser(ctx, username, "", email, "agent", nil); err != nil {
		return nil, err
	}
	if err := svc.KeyAdd(ctx, username, pubLine); err != nil {
		return nil, err
	}

	snippet := fmt.Sprintf(
		"git config user.name %q\ngit config user.email %q\ngit config user.signingkey %q\ngit config gpg.format ssh\ngit config commit.gpgsign true\n",
		username, email, keyPath,
	)

	return &AgentSetup{
		Username:      username,
		Email:         email,
		PrivateKey:    keyPath,
		PublicKey:     keyPath + ".pub",
		PubLine:       pubLine,
		ConfigSnippet: snippet,
	}, nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// ProjectName reads refs/projects/meta/config's "project" field
// (docs/02-data-model.md「meta/config ref」, written by
// internal/app/initapp), falling back to "githive" if meta/config does not
// exist yet or the field is missing/malformed.
func ProjectName(ctx context.Context, dir string) string {
	const fallback = "githive"
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, "refs/projects/meta/config")
	if err != nil || oid == "" {
		return fallback
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return fallback
	}
	files, err := chain.ReadTree(repo, plumbing.NewHash(oid))
	if err != nil {
		return fallback
	}
	configJSON, ok := files["config.json"]
	if !ok {
		return fallback
	}
	decoded, err := event.DecodeGeneric(configJSON)
	if err != nil {
		return fallback
	}
	config, ok := decoded.(map[string]any)
	if !ok {
		return fallback
	}
	project, ok := config["project"].(string)
	if !ok || project == "" {
		return fallback
	}
	return project
}
