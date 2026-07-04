package materialize

import "github.com/ymsaki/githive/internal/core/event"

// TaskRegistry is the fold registry for the task feature
// (docs/features/task.md), ported from spec/reference/fold_task.py.
var TaskRegistry = newTaskRegistry()

var taskStatuses = map[string]bool{
	"todo": true, "doing": true, "review": true, "done": true, "blocked": true, "cancelled": true,
}

var taskAllowedTransitions = map[[2]string]bool{
	{"todo", "doing"}:    true,
	{"doing", "review"}:  true,
	{"review", "done"}:   true,
	{"doing", "blocked"}: true,
	{"blocked", "doing"}: true,
}

func taskValidTransition(from string, hasFrom bool, to string) bool {
	if !taskStatuses[to] {
		return false
	}
	if !hasFrom {
		return true
	}
	if to == "cancelled" {
		return true
	}
	if to == "todo" {
		return from != "todo"
	}
	return taskAllowedTransitions[[2]string{from, to}]
}

func taskLinks(s *State) []map[string]any {
	l, _ := s.Scratch["links"].([]map[string]any)
	return l
}

func setTaskLinks(s *State, links []map[string]any) {
	s.Scratch["links"] = links
	s.Meta["links"] = linksToAny(links)
}

func taskStatusHistory(s *State) []map[string]any {
	h, _ := s.Scratch["status_history"].([]map[string]any)
	return h
}

func appendTaskStatusHistory(s *State, entry map[string]any) {
	h := append(taskStatusHistory(s), entry)
	s.Scratch["status_history"] = h
	out := make([]any, len(h))
	for i, e := range h {
		out[i] = e
	}
	s.Meta["status_history"] = out
}

func newTaskRegistry() *Registry {
	r := NewRegistry()

	r.Register("task.create", func(s *State, env *event.Envelope) {
		if s.Meta != nil {
			return
		}
		data := env.Data
		owner := stringOr(data["owner"], "")
		if owner == "" {
			owner = env.Actor
		}
		s.Meta = map[string]any{
			"id":         env.Entity,
			"title":      stringOr(data["title"], ""),
			"status":     "todo",
			"owner":      owner,
			"created_by": env.Actor,
			"created_at": env.TS,
			"updated_at": env.TS,
			"links":      []any{},
		}
		s.Collections["notes"] = map[string]any{}
		appendTaskStatusHistory(s, map[string]any{"to": "todo", "by": env.Actor, "ts": env.TS})
		if body, ok := data["body"]; ok {
			s.Meta["body"] = body
		}
		if due, ok := data["due"]; ok {
			s.Meta["due"] = due
		}
		if priority, ok := data["priority"]; ok {
			s.Meta["priority"] = priority
		}
	})

	notCreated := func(s *State) bool { return s.Meta == nil }

	r.Register("task.edit", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		data := env.Data
		if title, ok := data["title"]; ok {
			s.Meta["title"] = title
		}
		if body, ok := data["body"]; ok {
			s.Meta["body"] = body
		}
		if due, ok := data["due"]; ok {
			s.Meta["due"] = due
		}
		if priority, ok := data["priority"]; ok {
			s.Meta["priority"] = priority
		}
		s.Meta["updated_at"] = env.TS
	})

	r.Register("task.status", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		to, ok := env.Data["to"].(string)
		if !ok {
			return
		}
		from, hasFrom := s.Meta["status"].(string)
		if !taskValidTransition(from, hasFrom, to) {
			return
		}
		s.Meta["status"] = to
		s.Meta["updated_at"] = env.TS
		appendTaskStatusHistory(s, map[string]any{"to": to, "by": env.Actor, "ts": env.TS})

		if note, ok := env.Data["note"].(string); ok && note != "" {
			s.Collections["notes"][env.ID] = map[string]any{
				"id":     env.ID,
				"author": env.Actor,
				"ts":     env.TS,
				"body":   note,
			}
		}
	})

	r.Register("task.reassign", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		if owner, ok := env.Data["owner"].(string); ok {
			s.Meta["owner"] = owner
			s.Meta["updated_at"] = env.TS
		}
	})

	r.Register("task.note", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		s.Collections["notes"][env.ID] = map[string]any{
			"id":     env.ID,
			"author": env.Actor,
			"ts":     env.TS,
			"body":   stringOr(env.Data["body"], ""),
		}
		s.Meta["updated_at"] = env.TS
	})

	r.Register("task.link", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		applyLinkEvent(s, env, taskLinks, setTaskLinks)
	})

	return r
}
