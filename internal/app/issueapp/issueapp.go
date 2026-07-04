package issueapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// ErrIdentityNotConfigured means git's user.email is unset
// (docs/02-data-model.md「actor」: 未設定ならエラー、終了コード 5).
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// Sentinel errors the CLI layer maps to exit codes
// (docs/01-architecture.md「エラー処理方針」, docs/10-cli-spec.md「終了コード」).
var (
	// ErrNotFound means no issue ref matched the given ID/prefix.
	ErrNotFound = errors.New("issueapp: issue not found")
	// ErrRetriesExhausted means a local CAS write kept losing to concurrent
	// writers (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」).
	ErrRetriesExhausted = errors.New("issueapp: local ref update retries exhausted")
)

// AmbiguousIDError is returned when a short ID prefix matches more than one
// issue (docs/10-cli-spec.md「ID の入力解決」).
type AmbiguousIDError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousIDError) Error() string {
	return fmt.Sprintf("issueapp: ambiguous id prefix %q matches %d issues", e.Prefix, len(e.Candidates))
}

const localWriteRetries = 10

// Meta is a convenience alias for the fold's meta.json-shaped map.
type Meta = map[string]any

// Comment is one folded comment.
type Comment = map[string]any

// Show is the full read result for githive issue show.
type Show struct {
	Meta     Meta
	Body     string
	Comments []Comment
}

// Service operates on issues within a single repository directory.
type Service struct {
	Dir string
}

// New returns a Service rooted at dir (a githive-managed git repository).
func New(dir string) *Service {
	return &Service{Dir: dir}
}

func refFor(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/projects/issue/" + id)
}

// currentEvents returns every event in the issue's chain (empty if the
// issue does not exist yet) along with the ref's current OID (gitx.ZeroOID
// if absent).
func (s *Service) currentEvents(ctx context.Context, id string) (events []*event.Envelope, oid string, err error) {
	r := gitx.New(s.Dir)
	oid, err = r.RevParse(ctx, refFor(id).String())
	if err != nil {
		return nil, "", err
	}
	if oid == "" {
		return nil, gitx.ZeroOID, nil
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, "", err
	}
	events, err = chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, "", err
	}
	return events, oid, nil
}

// appendEvent folds env on top of the issue's current events, writes a new
// commit, and CAS-advances the ref, retrying against concurrent local
// writers (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」,
// docs/14-testing.md シナリオ11). It returns the resulting state so callers
// can detect no-op writes (e.g. an invalid status transition, which fold
// silently ignores per docs/features/issue.md「ステータス機械」).
func (s *Service) appendEvent(ctx context.Context, id string, buildEvent func() (*event.Envelope, string)) (*materialize.State, error) {
	r := gitx.New(s.Dir)
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, err
	}
	sig, err := identity.Resolve(ctx, s.Dir)
	if err != nil {
		return nil, err
	}
	ref := refFor(id)

	for attempt := 0; attempt < localWriteRetries; attempt++ {
		existingEvents, oid, err := s.currentEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		env, summary := buildEvent()
		env.Actor = sig.Email
		allEvents := append(append([]*event.Envelope(nil), existingEvents...), env)
		state := materialize.IssueRegistry.Fold(allEvents)
		if state.Meta == nil {
			return nil, ErrNotFound
		}

		files, err := TreeFiles(state)
		if err != nil {
			return nil, err
		}
		var parent plumbing.Hash
		if oid != gitx.ZeroOID {
			parent = plumbing.NewHash(oid)
		}
		newHash, err := chain.AppendEvent(repo, parent, env, summary, files, sig)
		if err != nil {
			return nil, err
		}
		if err := r.UpdateRef(ctx, ref.String(), newHash.String(), oid); err != nil {
			if errors.Is(err, gitx.ErrRefCASMismatch) {
				continue // someone else advanced the ref first; reread and retry
			}
			return nil, err
		}
		return state, nil
	}
	return nil, ErrRetriesExhausted
}

// New creates a new issue and returns its ID.
func (s *Service) NewIssue(ctx context.Context, title, body string, labels, assignees []string) (string, error) {
	id := idgen.New()
	data := map[string]any{"title": title}
	if body != "" {
		data["body"] = body
	}
	if len(labels) > 0 {
		data["labels"] = toAnySlice(labels)
	}
	if len(assignees) > 0 {
		data["assignees"] = toAnySlice(assignees)
	}

	_, err := s.appendEvent(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: "issue.create", ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, "issue.create: " + title
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
