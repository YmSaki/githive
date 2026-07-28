package taskapp

import (
	"context"
	"errors"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// ErrIdentityNotConfigured means git's user.email is unset.
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// ErrNotFound means no task ref matched the given ID/prefix.
var ErrNotFound = errors.New("taskapp: task not found")

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」).
var ErrRetriesExhausted = entitychain.ErrRetriesExhausted

// Meta is a convenience alias for the fold's meta.json-shaped map.
type Meta = map[string]any

// Note is one folded note.
type Note = map[string]any

// Show is the full read result for githive task show.
type Show struct {
	Meta  Meta
	Body  string
	Notes []Note
}

// Service operates on tasks within a single repository directory.
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
			Registry:    materialize.TaskRegistry,
			TreeFiles:   TreeFiles,
			NotFoundErr: ErrNotFound,
		},
	}
}

func refFor(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/projects/task/" + id)
}

// NewTask creates a new task and returns its ID. owner defaults to the
// actor if empty (docs/features/task.md「イベント定義」).
func (s *Service) NewTask(ctx context.Context, title, body, owner, due, priority string) (string, error) {
	id := idgen.New()
	data := map[string]any{"title": title}
	if body != "" {
		data["body"] = body
	}
	if owner != "" {
		data["owner"] = owner
	}
	if due != "" {
		data["due"] = due
	}
	if priority != "" {
		data["priority"] = priority
	}

	_, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: event.SchemaVersion, Kind: "task.create", ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, "task.create: " + title
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
