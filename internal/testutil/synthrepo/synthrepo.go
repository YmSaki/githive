// Package synthrepo builds deterministic, parameterised githive repositories
// for benchmarks and scale tests without shelling out to `git` per ref.
package synthrepo

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/projectconfig"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Config controls the size and shape of a generated repository.
type Config struct {
	Issues               int
	CommentsPerIssue     int
	MirrorRemoteTracking bool
	InitBareRemote       bool
	SignWithEd25519      bool
}

// Result holds the paths and refs created by Generate.
type Result struct {
	Dir       string
	RemoteDir string
	IssueRefs []string
}

const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// deterministicULID returns a valid lowercase 26-char ULID for seq >= 0.
func deterministicULID(seq int64) string {
	var buf [26]byte
	for i := 25; i >= 0; i-- {
		buf[i] = crockford[seq%32]
		seq /= 32
	}
	return string(buf[:])
}

func deterministicTS(seq int64) string {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(seq) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

// Generate creates a githive repository under dir (which must be empty or
// non-existent) with Config.Issues issue chains that pass fsck.
func Generate(dir string, cfg Config) (*Result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("synthrepo: mkdir %s: %w", dir, err)
	}
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return nil, fmt.Errorf("synthrepo: init %s: %w", dir, err)
	}

	sig := object.Signature{
		Name:  "bench",
		Email: "bench@example.com",
		When:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	var commitOpts []chain.Option
	var sshSigner ssh.Signer
	var pubLine string
	if cfg.SignWithEd25519 {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: generate ed25519 key: %w", err)
		}
		sshSigner, err = ssh.NewSignerFromKey(priv)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: ssh signer: %w", err)
		}
		pubLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshSigner.PublicKey())))
		commitOpts = append(commitOpts, chain.WithSigner(makeSSHSigSigner(sshSigner)))
	}

	var seq int64 = 1
	nextID := func() (string, string) {
		id := deterministicULID(seq)
		ts := deterministicTS(seq)
		seq++
		return id, ts
	}

	// Seed refs/projects/meta/config with all required meta.init fields.
	cfgID, cfgTS := nextID()
	cfgData := map[string]any{
		"schema_version": projectconfig.SchemaVersion,
		"project":        "synth-bench",
		"features":       []any{"issue", "task", "notify", "users", "chat", "wiki"},
		"created_at":     cfgTS,
	}
	cfgEnv := &event.Envelope{
		V:      event.SchemaVersion,
		Kind:   "meta.init",
		ID:     cfgID,
		TS:     cfgTS,
		Actor:  sig.Email,
		Entity: cfgID,
		Data:   cfgData,
	}
	cfgBytes, err := event.Encode(cfgData)
	if err != nil {
		return nil, fmt.Errorf("synthrepo: encode config: %w", err)
	}
	cfgHash, err := chain.AppendEvent(repo, chain.ZeroHash, cfgEnv, "meta: init", map[string][]byte{
		"config.json": cfgBytes,
	}, sig, commitOpts...)
	if err != nil {
		return nil, fmt.Errorf("synthrepo: append meta/config: %w", err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(refspace.MetaConfigRef), cfgHash)); err != nil {
		return nil, fmt.Errorf("synthrepo: set meta/config ref: %w", err)
	}
	if cfg.MirrorRemoteTracking {
		tr, err := refspace.RemoteTrackingRef(refspace.MetaConfigRef)
		if err != nil {
			return nil, err
		}
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(tr), cfgHash)); err != nil {
			return nil, err
		}
	}

	// Seed refs/projects/users/registry when SignWithEd25519 is enabled.
	if cfg.SignWithEd25519 {
		regEvents := []*event.Envelope{}
		uID, uTS := nextID()
		uEnv := &event.Envelope{
			V:      event.SchemaVersion,
			Kind:   "users.user_set",
			ID:     uID,
			TS:     uTS,
			Actor:  sig.Email,
			Entity: uID,
			Data: map[string]any{
				"username": "benchadmin",
				"fields": map[string]any{
					"emails": []any{sig.Email},
					"roles":  []any{"admin"},
					"kind":   "human",
					"status": "active",
				},
			},
		}
		regEvents = append(regEvents, uEnv)
		regFiles, err := renderUsersTree(materialize.UsersRegistry.Fold(regEvents))
		if err != nil {
			return nil, err
		}
		regHead, err := chain.AppendEvent(repo, chain.ZeroHash, uEnv, "users: add benchadmin", regFiles, sig, commitOpts...)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: user_set: %w", err)
		}

		kID, kTS := nextID()
		kEnv := &event.Envelope{
			V:      event.SchemaVersion,
			Kind:   "users.key_add",
			ID:     kID,
			TS:     kTS,
			Actor:  sig.Email,
			Entity: kID,
			Data: map[string]any{
				"username": "benchadmin",
				"pub":      pubLine,
			},
		}
		regEvents = append(regEvents, kEnv)
		regFiles, err = renderUsersTree(materialize.UsersRegistry.Fold(regEvents))
		if err != nil {
			return nil, err
		}
		regHead, err = chain.AppendEvent(repo, regHead, kEnv, "users: key_add", regFiles, sig, commitOpts...)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: key_add: %w", err)
		}
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(refspace.UsersRegistryRef), regHead)); err != nil {
			return nil, fmt.Errorf("synthrepo: set users/registry ref: %w", err)
		}
		if cfg.MirrorRemoteTracking {
			tr, err := refspace.RemoteTrackingRef(refspace.UsersRegistryRef)
			if err != nil {
				return nil, err
			}
			if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(tr), regHead)); err != nil {
				return nil, err
			}
		}
	}

	res := &Result{
		Dir:       dir,
		IssueRefs: make([]string, 0, cfg.Issues),
	}

	for i := 0; i < cfg.Issues; i++ {
		issueID, createTS := nextID()
		createEnv := &event.Envelope{
			V:      event.SchemaVersion,
			Kind:   "issue.create",
			ID:     issueID,
			TS:     createTS,
			Actor:  sig.Email,
			Entity: issueID,
			Data: map[string]any{
				"title":  fmt.Sprintf("Synthetic issue %d", i),
				"body":   "Synthetic issue body for benchmarking.",
				"labels": []any{"bench"},
			},
		}
		issueEvents := []*event.Envelope{createEnv}
		files, err := renderIssueTree(materialize.IssueRegistry.Fold(issueEvents))
		if err != nil {
			return nil, err
		}
		head, err := chain.AppendEvent(repo, chain.ZeroHash, createEnv, "issue: create", files, sig, commitOpts...)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: create issue %d: %w", i, err)
		}

		for c := 0; c < cfg.CommentsPerIssue; c++ {
			cid, cts := nextID()
			cEnv := &event.Envelope{
				V:      event.SchemaVersion,
				Kind:   "issue.comment",
				ID:     cid,
				TS:     cts,
				Actor:  sig.Email,
				Entity: issueID,
				Data: map[string]any{
					"body": fmt.Sprintf("Comment %d on issue %d", c, i),
				},
			}
			issueEvents = append(issueEvents, cEnv)
			files, err = renderIssueTree(materialize.IssueRegistry.Fold(issueEvents))
			if err != nil {
				return nil, err
			}
			head, err = chain.AppendEvent(repo, head, cEnv, "issue: comment", files, sig, commitOpts...)
			if err != nil {
				return nil, fmt.Errorf("synthrepo: comment %d on issue %d: %w", c, i, err)
			}
		}

		refName, err := refspace.EntityRef(refspace.FeatureIssue, issueID)
		if err != nil {
			return nil, fmt.Errorf("synthrepo: entity ref %s: %w", issueID, err)
		}
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(refName), head)); err != nil {
			return nil, fmt.Errorf("synthrepo: set ref %s: %w", refName, err)
		}
		if cfg.MirrorRemoteTracking {
			tr, err := refspace.RemoteTrackingRef(refName)
			if err != nil {
				return nil, err
			}
			if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(tr), head)); err != nil {
				return nil, fmt.Errorf("synthrepo: set tracking ref %s: %w", tr, err)
			}
		}
		res.IssueRefs = append(res.IssueRefs, refName)
	}

	if cfg.InitBareRemote {
		remoteDir := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-remote.git")
		if _, err := git.PlainInit(remoteDir, true); err != nil {
			return nil, fmt.Errorf("synthrepo: init bare remote: %w", err)
		}
		res.RemoteDir = remoteDir
	}

	return res, nil
}

func renderIssueTree(state *materialize.State) (map[string][]byte, error) {
	files := map[string][]byte{}
	metaCopy := make(map[string]any, len(state.Meta))
	body := ""
	for k, v := range state.Meta {
		if k == "body" {
			if s, ok := v.(string); ok {
				body = s
			}
			continue
		}
		metaCopy[k] = v
	}
	metaJSON, err := event.Encode(metaCopy)
	if err != nil {
		return nil, err
	}
	files["meta.json"] = metaJSON
	files["body.md"] = []byte(body)
	for id, raw := range state.Collections["comments"] {
		comment, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var b strings.Builder
		b.WriteString("---\n")
		for _, pair := range [][2]string{{"author", "author"}, {"ts", "ts"}, {"id", "event"}} {
			if v, ok := comment[pair[0]].(string); ok && v != "" {
				b.WriteString(pair[1] + ": " + v + "\n")
			}
		}
		b.WriteString("---\n\n")
		if cb, ok := comment["body"].(string); ok {
			b.WriteString(cb)
			if !strings.HasSuffix(cb, "\n") {
				b.WriteString("\n")
			}
		}
		files["comments/"+id+".md"] = []byte(b.String())
	}
	return files, nil
}

func renderUsersTree(state *materialize.State) (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, raw := range state.Collections["users"] {
		b, err := event.Encode(raw)
		if err != nil {
			return nil, err
		}
		files["users/"+name+".json"] = b
	}
	for name, raw := range state.Collections["groups"] {
		b, err := event.Encode(raw)
		if err != nil {
			return nil, err
		}
		files["groups/"+name+".json"] = b
	}
	return files, nil
}

func makeSSHSigSigner(signer ssh.Signer) chain.Signer {
	return func(payload []byte) (string, error) {
		sum := sha256.Sum256(payload)
		signed := append([]byte("SSHSIG"), ssh.Marshal(struct {
			Namespace     string
			Reserved      string
			HashAlgorithm string
			Hash          []byte
		}{
			Namespace:     "git",
			Reserved:      "",
			HashAlgorithm: "sha256",
			Hash:          sum[:],
		})...)
		sig, err := signer.Sign(rand.Reader, signed)
		if err != nil {
			return "", err
		}
		blob := append([]byte("SSHSIG"), ssh.Marshal(struct {
			Version       uint32
			PublicKey     []byte
			Namespace     string
			Reserved      string
			HashAlgorithm string
			Signature     []byte
		}{
			Version:       1,
			PublicKey:     signer.PublicKey().Marshal(),
			Namespace:     "git",
			Reserved:      "",
			HashAlgorithm: "sha256",
			Signature:     ssh.Marshal(sig),
		})...)
		b64 := base64.StdEncoding.EncodeToString(blob)
		var b strings.Builder
		b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
		for i := 0; i < len(b64); i += 70 {
			end := i + 70
			if end > len(b64) {
				end = len(b64)
			}
			b.WriteString(b64[i:end] + "\n")
		}
		b.WriteString("-----END SSH SIGNATURE-----")
		return b.String(), nil
	}
}
