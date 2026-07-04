package chatapp

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

// ErrIdentityNotConfigured means git's user.email is unset.
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// ErrNotFound means no chat ref matched the given ID/prefix.
var ErrNotFound = errors.New("chatapp: chat thread not found")

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers.
var ErrRetriesExhausted = entitychain.ErrRetriesExhausted

// AmbiguousIDError is returned when a short ID prefix matches more than one
// thread (docs/10-cli-spec.md「ID の入力解決」).
type AmbiguousIDError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousIDError) Error() string {
	return fmt.Sprintf("chatapp: ambiguous id prefix %q matches %d threads", e.Prefix, len(e.Candidates))
}

// Meta is a convenience alias for the fold's meta.json-shaped map.
type Meta = map[string]any

// Message is one folded message.
type Message = map[string]any

// Show is the full read result for githive chat show.
type Show struct {
	Meta     Meta
	Messages []Message
}

// Service operates on chat threads within a single repository directory.
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
			Registry:    materialize.ChatRegistry,
			TreeFiles:   TreeFiles,
			NotFoundErr: ErrNotFound,
		},
	}
}

func refFor(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/projects/chat/" + id)
}

// NewThread creates a new chat thread and returns its ID. If body is
// non-empty it doubles as the thread's first message
// (docs/features/chat.md「イベント定義」).
func (s *Service) NewThread(ctx context.Context, title, body string) (string, error) {
	id := idgen.New()
	data := map[string]any{"title": title}
	if body != "" {
		data["body"] = body
	}

	_, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: "chat.create", ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, "chat.create: " + title
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) mutate(ctx context.Context, id, kind, summary string, data map[string]any) error {
	_, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: kind, ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, summary
	})
	return err
}

// Post adds a message. If supersedes is non-empty, the fold treats this as
// replacing that earlier message's displayed content.
func (s *Service) Post(ctx context.Context, id, body, replyTo, supersedes string) error {
	data := map[string]any{"body": body}
	if replyTo != "" {
		data["reply_to"] = replyTo
	}
	if supersedes != "" {
		data["supersedes"] = supersedes
	}
	return s.mutate(ctx, id, "chat.post", "chat.post: "+summarize(body), data)
}

// Archive sets the thread's status to archived.
func (s *Service) Archive(ctx context.Context, id string) error {
	return s.mutate(ctx, id, "chat.edit_meta", "chat.edit_meta: archived", map[string]any{"status": "archived"})
}

// EditMeta overwrites title/status (LWW). Pass empty string to leave a
// field unchanged.
func (s *Service) EditMeta(ctx context.Context, id, title, status string) error {
	data := map[string]any{}
	if title != "" {
		data["title"] = title
	}
	if status != "" {
		data["status"] = status
	}
	return s.mutate(ctx, id, "chat.edit_meta", "chat.edit_meta", data)
}

func summarize(body string) string {
	runes := []rune(body)
	if len(runes) > 40 {
		return string(runes[:40])
	}
	return body
}
