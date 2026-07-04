package materialize

import (
	"sort"

	"github.com/ymsaki/githive/internal/core/event"
)

// NotifyRegistry is the fold registry for the notify feature
// (docs/features/notify.md). Unlike issue/task/chat, notify/stream is a
// singleton, ever-growing log with no "create" event; Meta is seeded to an
// empty object by the first accepted event instead of gating on a create.
var NotifyRegistry = newNotifyRegistry()

func ensureNotifyMeta(s *State) {
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	if s.Collections["posts"] == nil {
		s.Collections["posts"] = map[string]any{}
	}
}

func notifyAcks(s *State) map[string][]string {
	m, _ := s.Scratch["acks"].(map[string][]string)
	if m == nil {
		m = map[string][]string{}
		s.Scratch["acks"] = m
	}
	return m
}

func newNotifyRegistry() *Registry {
	r := NewRegistry()

	r.Register("notify.post", func(s *State, env *event.Envelope) {
		ensureNotifyMeta(s)
		data := env.Data
		post := map[string]any{
			"id":      env.ID,
			"ts":      env.TS,
			"actor":   env.Actor,
			"targets": data["targets"],
			"title":   stringOr(data["title"], ""),
			"month":   monthOf(env.TS),
		}
		if body, ok := data["body"]; ok {
			post["body"] = body
		}
		if source, ok := data["source"]; ok {
			post["source"] = source
		}
		if priority, ok := data["priority"]; ok {
			post["priority"] = priority
		}
		s.Collections["posts"][env.ID] = post
	})

	r.Register("notify.ack", func(s *State, env *event.Envelope) {
		ensureNotifyMeta(s)
		ackOf, ok := env.Data["ack_of"].(string)
		if !ok || ackOf == "" {
			return
		}
		acks := notifyAcks(s)
		list := acks[ackOf]
		for _, a := range list {
			if a == env.Actor {
				return // already acked, no-op
			}
		}
		list = append(list, env.Actor)
		sort.Strings(list)
		acks[ackOf] = list
	})

	return r
}

// monthOf extracts the "YYYY-MM" bucket a timestamp belongs to, used to
// group notify.post records into events/<month>.jsonl
// (docs/features/notify.md「ref とツリー」).
func monthOf(ts string) string {
	if len(ts) < 7 {
		return ts
	}
	return ts[:7]
}

// NotifyAcks exposes the fold's internal ack map (ack_of -> sorted actor
// list) for app-layer tree writers, since it lives outside the generic
// Meta/Collections shape (docs/features/notify.md「acks.json」).
func NotifyAcks(s *State) map[string][]string {
	m, _ := s.Scratch["acks"].(map[string][]string)
	return m
}
