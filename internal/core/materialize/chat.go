package materialize

import "github.com/ymsaki/githive/internal/core/event"

// ChatRegistry is the fold registry for the chat feature
// (docs/features/chat.md).
var ChatRegistry = newChatRegistry()

func chatParticipants(s *State) map[string]bool {
	m, _ := s.Scratch["participants"].(map[string]bool)
	if m == nil {
		m = map[string]bool{}
		s.Scratch["participants"] = m
	}
	return m
}

func addChatParticipant(s *State, actor string) {
	participants := chatParticipants(s)
	participants[actor] = true
	s.Meta["participants"] = sortedKeysAny(participants)
}

func newChatRegistry() *Registry {
	r := NewRegistry()

	r.Register("chat.create", func(s *State, env *event.Envelope) {
		if s.Meta != nil {
			// Only the chain's first create is accepted; later ones are
			// ignored as invalid, mirroring issue/task.create.
			return
		}
		data := env.Data
		s.Meta = map[string]any{
			"id":            env.Entity,
			"title":         stringOr(data["title"], ""),
			"status":        "open",
			"created_by":    env.Actor,
			"created_at":    env.TS,
			"updated_at":    env.TS,
			"message_count": 0,
			"participants":  []any{},
		}
		s.Collections["messages"] = map[string]any{}
		addChatParticipant(s, env.Actor)

		// A create with a body doubles as the thread's first message
		// (docs/features/chat.md「イベント定義」).
		if body, ok := data["body"].(string); ok && body != "" {
			s.Collections["messages"][env.ID] = map[string]any{
				"id":     env.ID,
				"author": env.Actor,
				"ts":     env.TS,
				"body":   body,
			}
			s.Meta["message_count"] = len(s.Collections["messages"])
		}
	})

	notCreated := func(s *State) bool { return s.Meta == nil }

	r.Register("chat.post", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		data := env.Data
		message := map[string]any{
			"id":     env.ID,
			"author": env.Actor,
			"ts":     env.TS,
			"body":   stringOr(data["body"], ""),
		}
		if replyTo, ok := data["reply_to"]; ok {
			message["reply_to"] = replyTo
		}
		if supersedes, ok := data["supersedes"].(string); ok && supersedes != "" {
			message["supersedes"] = supersedes
			if prior, ok := s.Collections["messages"][supersedes].(map[string]any); ok {
				prior["superseded_by"] = env.ID
			}
		}
		s.Collections["messages"][env.ID] = message
		s.Meta["message_count"] = len(s.Collections["messages"])
		addChatParticipant(s, env.Actor)
		s.Meta["updated_at"] = env.TS
	})

	r.Register("chat.edit_meta", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		data := env.Data
		if title, ok := data["title"]; ok {
			s.Meta["title"] = title
		}
		if status, ok := data["status"].(string); ok && (status == "open" || status == "archived") {
			s.Meta["status"] = status
		}
		s.Meta["updated_at"] = env.TS
	})

	return r
}
