package notifyapp

import (
	"context"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// ErrIdentityNotConfigured means git's user.email is unset.
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers.
var ErrRetriesExhausted = entitychain.ErrRetriesExhausted

// StreamRef is the notify feature's single ref
// (docs/02-data-model.md「ref 名前空間」).
const StreamRef = "refs/projects/notify/stream"

// Post is one folded notification.
type Post = map[string]any

// Service operates on the notify stream within a single repository
// directory.
type Service struct {
	Dir    string
	writer *entitychain.Writer
}

// New returns a Service rooted at dir (a githive-managed git repository).
func New(dir string) *Service {
	return &Service{
		Dir: dir,
		writer: &entitychain.Writer{
			Dir: dir,
			RefFor: func(string) plumbing.ReferenceName {
				return plumbing.ReferenceName(StreamRef)
			},
			Registry:  materialize.NotifyRegistry,
			TreeFiles: TreeFiles,
			// No NotFoundErr: the stream is a singleton with no "create"
			// event, so there is no not-found concept to enforce.
		},
	}
}

// Post appends a notify.post event. targets are "user:<email>",
// "group:<name>", or "all" strings (docs/features/notify.md「notify.post
// の data」). source, if non-nil, should have "kind" and "id" keys.
func (s *Service) Post(ctx context.Context, targets []string, title, body string, source map[string]any, priority string) (string, error) {
	data := map[string]any{
		"targets": toAnySlice(targets),
		"title":   title,
	}
	if body != "" {
		data["body"] = body
	}
	if source != nil {
		data["source"] = source
	}
	if priority != "" {
		data["priority"] = priority
	}

	var newID string
	_, err := s.writer.Append(ctx, "", func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		newID = eid
		return &event.Envelope{
			V: 1, Kind: "notify.post", ID: eid, TS: ts,
			Entity: eid, Data: data, Extra: map[string]any{},
		}, "notify.post: " + title
	})
	if err != nil {
		return "", err
	}
	return newID, nil
}

// Ack marks each of ackOf as read by the current actor, one event per ID
// (docs/features/notify.md「イベント定義」).
func (s *Service) Ack(ctx context.Context, ackOf []string) error {
	for _, id := range ackOf {
		targetID := id
		_, err := s.writer.Append(ctx, "", func() (*event.Envelope, string) {
			eid, ts := idgen.NewWithTimestamp()
			return &event.Envelope{
				V: 1, Kind: "notify.ack", ID: eid, TS: ts,
				Entity: eid, Data: map[string]any{"ack_of": targetID}, Extra: map[string]any{},
			}, "notify.ack: " + targetID
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
