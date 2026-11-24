// Package synthrepo builds deterministic, parameterised githive repositories
// for benchmarks and scale tests without shelling out to `git` per ref.
package synthrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/projectconfig"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Config controls the size and shape of a generated repository.
type Config struct {
	Issues               int
	CommentsPerIssue     int
	MirrorRemoteTracking bool
	InitBareRemote       bool
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
// non-existent) with Config.Issues issue chains.
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

	var seq int64 = 1
	nextID := func() (string, string) {
		id := deterministicULID(seq)
		ts := deterministicTS(seq)
		seq++
		return id, ts
	}

	// Seed refs/projects/meta/config so CheckSchemaSupported passes.
	cfgID, cfgTS := nextID()
	cfgEnv := &event.Envelope{
		V:      event.SchemaVersion,
		Kind:   "meta.init",
		ID:     cfgID,
		TS:     cfgTS,
		Actor:  sig.Email,
		Entity: cfgID,
		Data: map[string]any{
			"schema_version": projectconfig.SchemaVersion,
		},
	}
	cfgBytes, err := event.Encode(map[string]any{
		"schema_version": projectconfig.SchemaVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("synthrepo: encode config: %w", err)
	}
	cfgHash, err := chain.AppendEvent(repo, chain.ZeroHash, cfgEnv, "meta: init", map[string][]byte{
		"config.json": cfgBytes,
	}, sig)
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
		metaMap := map[string]any{
			"id":         issueID,
			"title":      fmt.Sprintf("Synthetic issue %d", i),
			"status":     "open",
			"labels":     []any{"bench"},
			"assignees":  []any{},
			"created_at": createTS,
			"updated_at": createTS,
			"author":     sig.Email,
		}
		metaBytes, err := event.Encode(metaMap)
		if err != nil {
			return nil, err
		}
		files := map[string][]byte{
			"meta.json": metaBytes,
			"body.md":   []byte("Synthetic issue body for benchmarking.\n"),
		}
		head, err := chain.AppendEvent(repo, chain.ZeroHash, createEnv, "issue: create", files, sig)
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
			metaMap["updated_at"] = cts
			metaBytes, err = event.Encode(metaMap)
			if err != nil {
				return nil, err
			}
			files["meta.json"] = metaBytes
			commentBytes, err := event.Encode(map[string]any{
				"id":     cid,
				"author": sig.Email,
				"ts":     cts,
				"body":   fmt.Sprintf("Comment %d on issue %d", c, i),
			})
			if err != nil {
				return nil, err
			}
			files[fmt.Sprintf("comments/%s.json", cid)] = commentBytes
			head, err = chain.AppendEvent(repo, head, cEnv, "issue: comment", files, sig)
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
