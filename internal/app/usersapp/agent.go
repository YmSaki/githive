package usersapp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
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
	return AddAgentWithProfile(ctx, dir, username, project, email, keyDir, AgentProfile{})
}

// AgentProfile contains the optional display name and roles supplied at setup.
type AgentProfile struct {
	Display string
	Roles   []string
}

// AddAgentWithProfile creates an agent and records its requested profile in the
// same initial identity event, preserving the identity and key safety guards.
func AddAgentWithProfile(ctx context.Context, dir, username, project, email, keyDir string, profile AgentProfile) (*AgentSetup, error) {
	if !ValidUsername(username) {
		return nil, fmt.Errorf("%w: username %q", ErrInvalidName, username)
	}
	if project == "" {
		project = ProjectName(ctx, dir)
	}
	if email == "" {
		if !agentProjectRE.MatchString(project) {
			return nil, fmt.Errorf("%w: project %q must be a lowercase DNS label (1-63 characters)", ErrInvalidName, project)
		}
		email = fmt.Sprintf("%s@agents.%s.invalid", username, project)
	}
	if !ValidEmail(email) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEmail, email)
	}
	svc := New(dir)
	nameFree := func(state *materialize.State) error {
		for _, collection := range []string{"users", "groups"} {
			if _, exists := state.Collections[collection][username]; exists {
				return fmt.Errorf("%w: %q is already registered", ErrNameExists, username)
			}
		}
		return nil
	}
	state, err := svc.writer.Fold(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := nameFree(state); err != nil {
		return nil, err
	}
	if _, err := identity.Resolve(ctx, dir); err != nil {
		return nil, err
	}
	if keyDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		keyDir = filepath.Join(home, ".ssh")
	}
	keyDir, err = filepath.Abs(keyDir)
	if err != nil {
		return nil, fmt.Errorf("usersapp: resolve key dir: %w", err)
	}
	// Qualify by project without putting untrusted project text in a filename.
	projectHash := sha256.Sum256([]byte(project))
	keyPath := filepath.Join(keyDir, fmt.Sprintf("githive-%s-%x", username, projectHash[:8]))
	for _, path := range []string{keyPath, keyPath + ".pub"} {
		if _, err := os.Lstat(path); err == nil {
			return nil, fmt.Errorf("%w: key file %s", ErrKeyExists, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("usersapp: inspect key file: %w", err)
		}
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return nil, fmt.Errorf("usersapp: ssh-keygen not found in PATH: %w", err)
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("usersapp: create key dir: %w", err)
	}
	stagingDir, err := os.MkdirTemp(keyDir, ".githive-agent-")
	if err != nil {
		return nil, fmt.Errorf("usersapp: create staging dir: %w", err)
	}
	defer os.RemoveAll(stagingDir)
	stagingKey := filepath.Join(stagingDir, "key")
	cmd := exec.CommandContext(ctx, "ssh-keygen",
		"-t", "ed25519",
		"-N", "",
		"-C", email,
		"-f", stagingKey,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("usersapp: ssh-keygen: %w: %s", err, out)
	}

	pubBytes, err := os.ReadFile(stagingKey + ".pub")
	if err != nil {
		return nil, fmt.Errorf("usersapp: read generated public key: %w", err)
	}
	pubLine := trimNewline(string(pubBytes))
	if err := ValidatePublicKey(pubLine); err != nil {
		return nil, err
	}
	privateBytes, err := os.ReadFile(stagingKey)
	if err != nil {
		return nil, fmt.Errorf("usersapp: read generated private key: %w", err)
	}
	if err := writeAgentKey(keyPath, privateBytes, 0o600); err != nil {
		return nil, err
	}
	keepKeys := false
	publicCreated := false
	defer func() {
		if !keepKeys {
			os.Remove(keyPath)
			if publicCreated {
				os.Remove(keyPath + ".pub")
			}
		}
	}()
	if err := writeAgentKey(keyPath+".pub", pubBytes, 0o644); err != nil {
		return nil, err
	}
	publicCreated = true
	fields := map[string]any{"emails": []any{email}, "kind": "agent"}
	if profile.Display != "" {
		fields["display"] = profile.Display
	}
	if profile.Roles != nil {
		roles := make([]any, len(profile.Roles))
		for i, role := range profile.Roles {
			roles[i] = role
		}
		fields["roles"] = roles
	}
	if err := svc.mutateChecked(ctx, "users.user_set", "users.user_set: "+username, map[string]any{
		"username": username,
		"fields":   fields,
	}, nameFree); err != nil {
		return nil, err
	}
	// Once identity registration is committed, retain the key even if the
	// following append fails: deleting it would make recovery harder.
	keepKeys = true
	if err := svc.KeyAdd(ctx, username, pubLine); err != nil {
		return nil, fmt.Errorf("usersapp: agent registered, key retained at %s; public key registration failed: %w", keyPath, err)
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

var agentProjectRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// O_EXCL also protects against another setup creating a file or symlink after
// the initial preflight check. Only files created by this call are removed.
func writeAgentKey(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: key file %s", ErrKeyExists, path)
		}
		return fmt.Errorf("usersapp: create key file: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(path)
		return fmt.Errorf("usersapp: write key file: %w", err)
	}
	return nil
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
