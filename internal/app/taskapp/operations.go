package taskapp

import (
	"context"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/idgen"
)

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

// Status requests a transition, optionally attaching a note. If the
// transition is invalid, the event is still recorded (for audit) but fold
// ignores it and ok reports false (docs/features/task.md「ステータス機械」).
func (s *Service) Status(ctx context.Context, id, to, note string) (ok bool, err error) {
	data := map[string]any{"to": to}
	if note != "" {
		data["note"] = note
	}
	state, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: "task.status", ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, "task.status: -> " + to
	})
	if err != nil {
		return false, err
	}
	status, _ := state.Meta["status"].(string)
	return status == to, nil
}

// Note appends a standalone note.
func (s *Service) Note(ctx context.Context, id, body string) error {
	return s.mutate(ctx, id, "task.note", "task.note: "+summarize(body), map[string]any{"body": body})
}

// Reassign changes the task's owner (LWW).
func (s *Service) Reassign(ctx context.Context, id, owner string) error {
	return s.mutate(ctx, id, "task.reassign", "task.reassign: "+owner, map[string]any{"owner": owner})
}

// Edit overwrites title/body/due/priority (LWW). Pass nil to leave a field
// unchanged.
func (s *Service) Edit(ctx context.Context, id string, title, body, due, priority *string) error {
	data := map[string]any{}
	if title != nil {
		data["title"] = *title
	}
	if body != nil {
		data["body"] = *body
	}
	if due != nil {
		data["due"] = *due
	}
	if priority != nil {
		data["priority"] = *priority
	}
	return s.mutate(ctx, id, "task.edit", "task.edit", data)
}

// Link adds or removes a {rel,id} link (rel is "task"/"issue"/"chat").
func (s *Service) Link(ctx context.Context, id, rel, linkedID string, remove bool) error {
	data := map[string]any{"rel": rel, "id": linkedID}
	if remove {
		data["remove"] = true
	}
	return s.mutate(ctx, id, "task.link", "task.link", data)
}

func summarize(body string) string {
	runes := []rune(body)
	if len(runes) > 40 {
		return string(runes[:40])
	}
	return body
}
