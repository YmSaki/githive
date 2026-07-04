package materialize

import (
	"sort"

	"github.com/ymsaki/githive/internal/core/event"
)

// IssueRegistry is the fold registry for the issue feature
// (docs/features/issue.md), ported from spec/reference/fold_issue.py.
var IssueRegistry = newIssueRegistry()

var issueStatuses = map[string]bool{
	"open": true, "in_progress": true, "resolved": true, "closed": true, "archived": true,
}

var issueAllowedTransitions = map[[2]string]bool{
	{"open", "in_progress"}:     true,
	{"in_progress", "resolved"}: true,
	{"resolved", "closed"}:      true,
	{"closed", "archived"}:      true,
}

func issueValidTransition(from string, hasFrom bool, to string) bool {
	if !issueStatuses[to] {
		return false
	}
	if !hasFrom {
		return true
	}
	if to == "closed" {
		return from != "archived"
	}
	if to == "open" {
		return from != "archived"
	}
	return issueAllowedTransitions[[2]string{from, to}]
}

func issueLabels(s *State) map[string]bool {
	m, _ := s.Scratch["labels"].(map[string]bool)
	if m == nil {
		m = map[string]bool{}
		s.Scratch["labels"] = m
	}
	return m
}

func issueAssignees(s *State) map[string]bool {
	m, _ := s.Scratch["assignees"].(map[string]bool)
	if m == nil {
		m = map[string]bool{}
		s.Scratch["assignees"] = m
	}
	return m
}

func issueLinks(s *State) []map[string]any {
	l, _ := s.Scratch["links"].([]map[string]any)
	return l
}

func setIssueLinks(s *State, links []map[string]any) {
	s.Scratch["links"] = links
	s.Meta["links"] = linksToAny(links)
}

func linksToAny(links []map[string]any) []any {
	out := make([]any, len(links))
	for i, l := range links {
		out[i] = l
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysAny(m map[string]bool) []any {
	keys := sortedKeys(m)
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

func newIssueRegistry() *Registry {
	r := NewRegistry()

	r.Register("issue.create", func(s *State, env *event.Envelope) {
		if s.Meta != nil {
			// Only the chain's first create is accepted; later ones are
			// ignored as invalid (spec/reference/fold_issue.py).
			return
		}
		data := env.Data
		labels := issueLabels(s)
		for _, v := range asStringSlice(data["labels"]) {
			labels[v] = true
		}
		assignees := issueAssignees(s)
		for _, v := range asStringSlice(data["assignees"]) {
			assignees[v] = true
		}
		s.Meta = map[string]any{
			"id":            env.Entity,
			"title":         stringOr(data["title"], ""),
			"status":        "open",
			"labels":        sortedKeysAny(labels),
			"assignees":     sortedKeysAny(assignees),
			"created_by":    env.Actor,
			"created_at":    env.TS,
			"updated_at":    env.TS,
			"closed_at":     nil,
			"comment_count": 0,
			"links":         []any{},
		}
		if body, ok := data["body"]; ok {
			s.Meta["body"] = body
		}
		s.Collections["comments"] = map[string]any{}
	})

	notCreated := func(s *State) bool { return s.Meta == nil }

	r.Register("issue.edit", func(s *State, env *event.Envelope) {
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
		s.Meta["updated_at"] = env.TS
	})

	r.Register("issue.status", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		to, ok := env.Data["to"].(string)
		if !ok {
			return
		}
		from, hasFrom := s.Meta["status"].(string)
		if !issueValidTransition(from, hasFrom, to) {
			return
		}
		s.Meta["status"] = to
		s.Meta["updated_at"] = env.TS
		if to == "closed" {
			s.Meta["closed_at"] = env.TS
		} else if to == "open" {
			s.Meta["closed_at"] = nil
		}
	})

	r.Register("issue.comment", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		data := env.Data
		comment := map[string]any{
			"id":     env.ID,
			"author": env.Actor,
			"ts":     env.TS,
			"body":   stringOr(data["body"], ""),
		}
		if replyTo, ok := data["reply_to"]; ok {
			comment["reply_to"] = replyTo
		}
		if supersedes, ok := data["supersedes"].(string); ok && supersedes != "" {
			comment["supersedes"] = supersedes
			if prior, ok := s.Collections["comments"][supersedes].(map[string]any); ok {
				prior["superseded_by"] = env.ID
			}
		}
		s.Collections["comments"][env.ID] = comment
		s.Meta["comment_count"] = len(s.Collections["comments"])
		s.Meta["updated_at"] = env.TS
	})

	r.Register("issue.label", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		labels := issueLabels(s)
		for _, v := range asStringSlice(env.Data["remove"]) {
			delete(labels, v)
		}
		for _, v := range asStringSlice(env.Data["add"]) {
			labels[v] = true
		}
		s.Meta["labels"] = sortedKeysAny(labels)
		s.Meta["updated_at"] = env.TS
	})

	r.Register("issue.assign", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		assignees := issueAssignees(s)
		for _, v := range asStringSlice(env.Data["remove"]) {
			delete(assignees, v)
		}
		for _, v := range asStringSlice(env.Data["add"]) {
			assignees[v] = true
		}
		s.Meta["assignees"] = sortedKeysAny(assignees)
		s.Meta["updated_at"] = env.TS
	})

	r.Register("issue.link", func(s *State, env *event.Envelope) {
		if notCreated(s) {
			return
		}
		applyLinkEvent(s, env, issueLinks, setIssueLinks)
	})

	return r
}

// applyLinkEvent implements the shared *.link fold rule (add/remove a
// {rel,id} pair, deduplicated) used identically by issue.link and task.link.
// Per spec/reference/fold_issue.py / fold_task.py, meta.links and
// meta.updated_at are always rewritten, even when add is a no-op duplicate
// or remove finds nothing to remove.
func applyLinkEvent(s *State, env *event.Envelope, get func(*State) []map[string]any, set func(*State, []map[string]any)) {
	rel, _ := env.Data["rel"].(string)
	id, _ := env.Data["id"].(string)
	remove, _ := env.Data["remove"].(bool)

	links := get(s)
	var out []map[string]any
	if remove {
		out = links[:0:0]
		for _, l := range links {
			if l["rel"] == rel && l["id"] == id {
				continue
			}
			out = append(out, l)
		}
	} else {
		out = links
		found := false
		for _, l := range links {
			if l["rel"] == rel && l["id"] == id {
				found = true
				break
			}
		}
		if !found {
			out = append(append([]map[string]any{}, links...), map[string]any{"rel": rel, "id": id})
		}
	}
	set(s, out)
	s.Meta["updated_at"] = env.TS
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func asStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
