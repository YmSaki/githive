package notifyapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/apperr"
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

// ErrNotFound means an acknowledgement target is not an existing post.
var ErrNotFound = errors.New("notifyapp: notification not found")

// StreamRef is the notify feature's single ref
// (docs/02-data-model.md「ref 名前空間」).
//
// Monthly shard refs (refs/projects/notify/<yyyy-mm>) are now recognized by
// refspace.Parse and are processed by sync/fsck/verify as ordinary notify
// chains, ahead of the actual sharding migration (D8,
// docs/features/notify.md「シャーディング（将来）」). This Service is
// intentionally still hard-coded to StreamRef, though: reading and writing
// across stream + shards - i.e. the reader-side aggregation and the
// meta/config-driven write-target switch - is deferred to that migration,
// not implemented here.
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
			V: event.SchemaVersion, Kind: "notify.post", ID: eid, TS: ts,
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
	_, err := s.AckIDs(ctx, ackOf)
	return err
}

// AckIDs resolves full IDs or unique prefixes (at least eight characters),
// acknowledges their posts, and returns full IDs for CLI/MCP output. Every
// input is resolved before any write, so an invalid batch appends no events.
func (s *Service) AckIDs(ctx context.Context, ackOf []string) ([]string, error) {
	posts, err := s.List(ctx, ListFilter{})
	if err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(ackOf))
	for _, input := range ackOf {
		prefix := strings.ToLower(input)
		var matches []string
		if len(prefix) >= 8 && len(prefix) <= 26 {
			for _, post := range posts {
				id, _ := post["id"].(string)
				if strings.HasPrefix(id, prefix) {
					matches = append(matches, id)
				}
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("%w: %q", ErrNotFound, input)
		case 1:
			resolved = append(resolved, matches[0])
		default:
			return nil, &apperr.AmbiguousIDError{Prefix: prefix, Candidates: matches}
		}
	}
	for _, id := range resolved {
		targetID := id
		_, err := s.writer.Append(ctx, "", func() (*event.Envelope, string) {
			eid, ts := idgen.NewWithTimestamp()
			return &event.Envelope{
				V: event.SchemaVersion, Kind: "notify.ack", ID: eid, TS: ts,
				Entity: eid, Data: map[string]any{"ack_of": targetID}, Extra: map[string]any{},
			}, "notify.ack: " + targetID
		})
		if err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
