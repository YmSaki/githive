package issueapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// ErrIdentityNotConfigured means git's user.email is unset
// (docs/02-data-model.md「actor」: 未設定ならエラー、終了コード 5).
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// ErrNotFound means no issue ref matched the given ID/prefix.
var ErrNotFound = errors.New("issueapp: issue not found")

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」).
var ErrRetriesExhausted = entitychain.ErrRetriesExhausted

// AmbiguousIDError is returned when a short ID prefix matches more than one
// issue (docs/10-cli-spec.md「ID の入力解決」).
type AmbiguousIDError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousIDError) Error() string {
	return fmt.Sprintf("issueapp: ambiguous id prefix %q matches %d issues", e.Prefix, len(e.Candidates))
}

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
	Dir    string
	writer *entitychain.Writer
}

// New returns a Service rooted at dir (a githive-managed git repository).
func New(dir string) *Service {
	return &Service{
		Dir: dir,
		writer: &entitychain.Writer{
			Dir:         dir,
			RefFor:      refFor,
			Registry:    materialize.IssueRegistry,
			TreeFiles:   TreeFiles,
			NotFoundErr: ErrNotFound,
		},
	}
}

func refFor(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/projects/issue/" + id)
}

// currentEvents returns every event in the issue's chain (empty if the
// issue does not exist yet) along with the ref's current OID.
func (s *Service) currentEvents(ctx context.Context, id string) ([]*event.Envelope, string, error) {
	return s.writer.CurrentEvents(ctx, id)
}

// NewIssue creates a new issue and returns its ID.
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

	_, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
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
