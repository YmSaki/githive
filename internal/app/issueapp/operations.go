package issueapp

import (
	"context"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/idgen"
)

func (s *Service) mutate(ctx context.Context, id, kind, summary string, data map[string]any) error {
	_, err := s.appendEvent(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: kind, ID: eid, TS: ts,
			Entity: id, Data: data, Extra: map[string]any{},
		}, summary
	})
	return err
}

// Comment adds a comment. If supersedes is non-empty, the fold treats this
// as replacing that earlier comment's displayed content
// (docs/features/issue.md「設計判断」).
func (s *Service) Comment(ctx context.Context, id, body, replyTo, supersedes string) error {
	data := map[string]any{"body": body}
	if replyTo != "" {
		data["reply_to"] = replyTo
	}
	if supersedes != "" {
		data["supersedes"] = supersedes
	}
	return s.mutate(ctx, id, "issue.comment", "issue.comment: "+summarize(body), data)
}

// Status requests a transition. If the transition is invalid, the event is
// still recorded (for audit) but fold ignores it and ok reports false
// (docs/features/issue.md「ステータス機械」: 不正な遷移イベントは fold 時に
// 無視する。エラーにせず検証コマンドで警告する).
func (s *Service) Status(ctx context.Context, id, to string) (ok bool, err error) {
	state, err := s.appendEvent(ctx, id, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: 1, Kind: "issue.status", ID: eid, TS: ts,
			Entity: id, Data: map[string]any{"to": to}, Extra: map[string]any{},
		}, "issue.status: -> " + to
	})
	if err != nil {
		return false, err
	}
	status, _ := state.Meta["status"].(string)
	return status == to, nil
}

// Edit overwrites title and/or body (LWW). Pass nil to leave a field
// unchanged.
func (s *Service) Edit(ctx context.Context, id string, title, body *string) error {
	data := map[string]any{}
	if title != nil {
		data["title"] = *title
	}
	if body != nil {
		data["body"] = *body
	}
	return s.mutate(ctx, id, "issue.edit", "issue.edit", data)
}

// Label applies add/remove to the label set (remove first, then add, per
// docs/features/issue.md).
func (s *Service) Label(ctx context.Context, id string, add, remove []string) error {
	data := map[string]any{}
	if len(add) > 0 {
		data["add"] = toAnySlice(add)
	}
	if len(remove) > 0 {
		data["remove"] = toAnySlice(remove)
	}
	return s.mutate(ctx, id, "issue.label", "issue.label", data)
}

// Assign applies add/remove to the assignee set.
func (s *Service) Assign(ctx context.Context, id string, add, remove []string) error {
	data := map[string]any{}
	if len(add) > 0 {
		data["add"] = toAnySlice(add)
	}
	if len(remove) > 0 {
		data["remove"] = toAnySlice(remove)
	}
	return s.mutate(ctx, id, "issue.assign", "issue.assign", data)
}

// Link adds or removes a {rel,id} link (rel is "task"/"issue"/"chat").
func (s *Service) Link(ctx context.Context, id, rel, linkedID string, remove bool) error {
	data := map[string]any{"rel": rel, "id": linkedID}
	if remove {
		data["remove"] = true
	}
	return s.mutate(ctx, id, "issue.link", "issue.link", data)
}

func summarize(body string) string {
	runes := []rune(body)
	if len(runes) > 40 {
		return string(runes[:40])
	}
	return body
}
