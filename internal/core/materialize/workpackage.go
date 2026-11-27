package materialize

import (
	"regexp"
	"sort"
	"time"

	"github.com/ymsaki/githive/internal/core/event"
)

// WorkpackageRegistry is the fold registry for the workpackage feature
// (refs/projects/workpackage/<id>), mirrored by
// spec/reference/fold_workpackage.py.
//
// State shape (the checkpoint data.state contract):
//   - Meta is exactly meta.json: every field always present, empty sets as
//     [] and unset scalars/objects as null. body is NOT kept in Meta.
//   - Collections["body"] = {"text": <raw body.md>}.
//   - Collections["notes"] = {<event-id>: {actor, body, event_id, kind, ts}}.
//   - Scratch is always empty.
//
// Every value stays JSON-shaped ([]any / map[string]any / string / nil) so a
// checkpoint snapshot round-trips byte-for-byte through StateToMap.
//
// No reducer looks at other refs or the wall clock; cross-ref derivations
// (ready, blocked, completed, ...) belong to internal/core/wpgraph.
var WorkpackageRegistry = newWorkpackageRegistry()

// WorkpackageLeaseMax bounds a claim's lease relative to the event's ts.
const WorkpackageLeaseMax = 7 * 24 * time.Hour

// WorkpackageTimeLayout is the stored UTC-millisecond timestamp layout.
const WorkpackageTimeLayout = "2006-01-02T15:04:05.000Z"

var wpCommitPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

var wpLinkRels = map[string]bool{"issue": true, "task": true, "chat": true, "workpackage": true}

// NormalizeLease parses an RFC 3339 lease (offsets allowed), converts it to
// UTC, floors it to the millisecond and clamps it to [ts, ts+7d]. ok is false
// when either input cannot be parsed.
func NormalizeLease(leaseUntil, ts string) (string, bool) {
	norm, ok := FloorLease(leaseUntil)
	if !ok {
		return "", false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return "", false
	}
	at = at.UTC()
	if norm.Before(at) {
		norm = at
	}
	if max := at.Add(WorkpackageLeaseMax); norm.After(max) {
		norm = max
	}
	return norm.Format(WorkpackageTimeLayout), true
}

// wpInstantPattern is the accepted RFC 3339 grammar, identical to
// spec/reference/fold_workpackage.py: upper-case T/Z, '.' fraction of 1-9
// digits, offset hours 00-23 and minutes 00-59. time.Parse alone is looser
// (',' fractions, >9 digits, +24:00), which would split Go and Python folds.
var wpInstantPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)

// FloorLease parses an RFC 3339 instant and floors it to UTC milliseconds,
// without clamping.
func FloorLease(value string) (time.Time, bool) {
	if !wpInstantPattern.MatchString(value) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	// Same range as Python's datetime: the UTC instant must be in 0001-9999.
	if u := t.UTC(); u.Year() >= 1 && u.Year() <= 9999 {
		return u.Truncate(time.Millisecond), true
	}
	return time.Time{}, false
}

func wpList(meta map[string]any, key string) []any {
	l, _ := meta[key].([]any)
	return l
}

func wpULID(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && event.IsValidULID(s)
}

func wpNonEmpty(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

func wpOptionalString(v any) any {
	if s, ok := wpNonEmpty(v); ok {
		return s
	}
	return nil
}

func wpAddNote(s *State, env *event.Envelope, body string) {
	s.Collections["notes"][env.ID] = map[string]any{
		"actor":    env.Actor,
		"body":     body,
		"event_id": env.ID,
		"kind":     env.Kind,
		"ts":       env.TS,
	}
}

// wpSortedIDs filters raw to distinct valid ULIDs other than self, sorted.
func wpSortedIDs(raw any, self string) []any {
	arr, _ := raw.([]any)
	seen := map[string]bool{}
	var ids []string
	for _, item := range arr {
		id, ok := wpULID(item)
		if !ok || id == self || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func wpLinkValid(rel, id any, self string) (string, string, bool) {
	r, ok := rel.(string)
	if !ok || !wpLinkRels[r] {
		return "", "", false
	}
	i, ok := wpULID(id)
	if !ok || (r == "workpackage" && i == self) {
		return "", "", false
	}
	return r, i, true
}

func wpSortLinks(links []any) {
	sort.Slice(links, func(a, b int) bool {
		la, lb := links[a].(map[string]any), links[b].(map[string]any)
		ra, rb := la["rel"].(string), lb["rel"].(string)
		if ra != rb {
			return ra < rb
		}
		return la["id"].(string) < lb["id"].(string)
	})
}

func wpHasLink(links []any, rel, id string) int {
	for i, raw := range links {
		l := raw.(map[string]any)
		if l["rel"] == rel && l["id"] == id {
			return i
		}
	}
	return -1
}

func wpFindClaim(meta map[string]any, claimID any) map[string]any {
	id, ok := claimID.(string)
	if !ok {
		return nil
	}
	for _, raw := range wpList(meta, "claims") {
		if c, ok := raw.(map[string]any); ok && c["claim_id"] == id {
			return c
		}
	}
	return nil
}

func newWorkpackageRegistry() *Registry {
	r := NewRegistry()

	r.Register("workpackage.create", func(s *State, env *event.Envelope) {
		if s.Meta != nil {
			return // only the chain's first create seeds the state
		}
		data := env.Data
		priority := "normal"
		if p, ok := data["priority"].(string); ok && (p == "low" || p == "normal" || p == "high") {
			priority = p
		}
		kind := "work"
		if k, ok := data["kind"].(string); ok && k == "container" {
			kind = k
		}
		var parent any
		if p, ok := wpULID(data["parent_id"]); ok && p != env.Entity {
			parent = p
		}
		var findingOf any
		if f, ok := data["finding_of"].(map[string]any); ok && kind == "work" {
			wp, okWP := wpULID(f["workpackage"])
			sub, okSub := wpULID(f["submit_id"])
			if okWP && okSub && wp != env.Entity {
				findingOf = map[string]any{"submit_id": sub, "workpackage": wp}
			}
		}
		links := []any{}
		rawLinks, _ := data["links"].([]any)
		for _, raw := range rawLinks {
			l, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			rel, id, ok := wpLinkValid(l["rel"], l["id"], env.Entity)
			if !ok || wpHasLink(links, rel, id) >= 0 {
				continue
			}
			links = append(links, map[string]any{"id": id, "rel": rel})
		}
		wpSortLinks(links)
		body, _ := data["body"].(string)

		s.Meta = map[string]any{
			"acceptance_version":    env.ID,
			"acceptances":           []any{},
			"cancellations":         []any{},
			"claims":                []any{},
			"created_at":            env.TS,
			"created_by":            env.Actor,
			"current_submission_id": nil,
			"depends_on":            wpSortedIDs(data["depends_on"], env.Entity),
			"finding_of":            findingOf,
			"id":                    env.Entity,
			"kind":                  kind,
			"links":                 links,
			"parent_id":             parent,
			"priority":              priority,
			"submissions":           []any{},
			"title":                 stringOr(data["title"], ""),
			"updated_at":            env.TS,
			"waivers":               []any{},
		}
		s.Collections["body"] = map[string]any{"text": body}
		s.Collections["notes"] = map[string]any{}
	})

	created := func(s *State) bool { return s.Meta != nil }
	touch := func(s *State, env *event.Envelope) { s.Meta["updated_at"] = env.TS }

	r.Register("workpackage.edit", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		data := env.Data
		changed := false
		if title, ok := wpNonEmpty(data["title"]); ok && title != s.Meta["title"] {
			s.Meta["title"] = title
			changed = true
		}
		if p, ok := data["priority"].(string); ok && (p == "low" || p == "normal" || p == "high") && p != s.Meta["priority"] {
			s.Meta["priority"] = p
			changed = true
		}
		if raw, present := data["parent_id"]; present {
			if raw == nil {
				if s.Meta["parent_id"] != nil {
					s.Meta["parent_id"] = nil
					changed = true
				}
			} else if p, ok := wpULID(raw); ok && p != env.Entity && p != s.Meta["parent_id"] {
				s.Meta["parent_id"] = p
				changed = true
			}
		}
		if body, ok := data["body"].(string); ok && body != s.Collections["body"]["text"] {
			s.Collections["body"]["text"] = body
			s.Meta["acceptance_version"] = env.ID
			changed = true
		}
		if changed {
			touch(s, env)
		}
	})

	r.Register("workpackage.dependency", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		target, ok := wpULID(env.Data["target"])
		if !ok || target == env.Entity {
			return
		}
		remove, _ := env.Data["remove"].(bool)
		deps := wpList(s.Meta, "depends_on")
		idx := -1
		for i, d := range deps {
			if d == target {
				idx = i
			}
		}
		switch {
		case remove && idx >= 0:
			next := append(append([]any{}, deps[:idx]...), deps[idx+1:]...)
			s.Meta["depends_on"] = next
		case !remove && idx < 0:
			s.Meta["depends_on"] = wpSortedIDs(append(append([]any{}, deps...), target), env.Entity)
		default:
			return
		}
		touch(s, env)
	})

	r.Register("workpackage.claim", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		lease, ok := env.Data["lease_until"].(string)
		if !ok {
			return
		}
		clamped, ok := NormalizeLease(lease, env.TS)
		if !ok {
			return
		}
		s.Meta["claims"] = append(append([]any{}, wpList(s.Meta, "claims")...), map[string]any{
			"branch":      wpOptionalString(env.Data["branch"]),
			"claim_id":    env.ID,
			"holder":      env.Actor,
			"lease_until": clamped,
			"released_at": nil,
			"ts":          env.TS,
		})
		touch(s, env)
	})

	r.Register("workpackage.renew", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		c := wpFindClaim(s.Meta, env.Data["claim_id"])
		if c == nil || c["holder"] != env.Actor || c["released_at"] != nil {
			return
		}
		current, _ := c["lease_until"].(string)
		if env.TS > current {
			return // expired: stored timestamps share one sortable layout
		}
		lease, ok := env.Data["lease_until"].(string)
		if !ok {
			return
		}
		clamped, ok := NormalizeLease(lease, env.TS)
		if !ok {
			return
		}
		changed := false
		if clamped != current {
			c["lease_until"] = clamped
			changed = true
		}
		if branch, ok := wpNonEmpty(env.Data["branch"]); ok && branch != c["branch"] {
			c["branch"] = branch
			changed = true
		}
		if changed {
			touch(s, env)
		}
	})

	r.Register("workpackage.release", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		c := wpFindClaim(s.Meta, env.Data["claim_id"])
		if c == nil || c["holder"] != env.Actor || c["released_at"] != nil {
			return
		}
		c["released_at"] = env.TS
		if note, ok := wpNonEmpty(env.Data["note"]); ok {
			wpAddNote(s, env, note)
		}
		touch(s, env)
	})

	r.Register("workpackage.submit", func(s *State, env *event.Envelope) {
		if !created(s) || s.Meta["kind"] == "container" {
			return
		}
		var commit any
		if c, ok := env.Data["commit"].(string); ok && wpCommitPattern.MatchString(c) {
			commit = c
		}
		var version any
		if v, ok := env.Data["acceptance_version"].(string); ok {
			version = v
		}
		var reviewers any
		if raw, ok := env.Data["reviewers"].([]any); ok {
			seen := map[string]bool{}
			var list []string
			for _, item := range raw {
				if r, ok := wpNonEmpty(item); ok && !seen[r] {
					seen[r] = true
					list = append(list, r)
				}
			}
			if len(list) > 0 {
				sort.Strings(list)
				arr := make([]any, len(list))
				for i, r := range list {
					arr[i] = r
				}
				reviewers = arr
			}
		}
		s.Meta["submissions"] = append(append([]any{}, wpList(s.Meta, "submissions")...), map[string]any{
			"acceptance_version": version,
			"branch":             wpOptionalString(env.Data["branch"]),
			"by":                 env.Actor,
			"commit":             commit,
			"event_id":           env.ID,
			"pr_url":             wpOptionalString(env.Data["pr_url"]),
			"reviewers":          reviewers,
			"ts":                 env.TS,
		})
		s.Meta["current_submission_id"] = env.ID
		if note, ok := wpNonEmpty(env.Data["note"]); ok {
			wpAddNote(s, env, note)
		}
		touch(s, env)
	})

	r.Register("workpackage.accept", func(s *State, env *event.Envelope) {
		if !created(s) || s.Meta["kind"] == "container" {
			return
		}
		submitID, ok := wpULID(env.Data["submit_id"])
		if !ok {
			return
		}
		s.Meta["acceptances"] = append(append([]any{}, wpList(s.Meta, "acceptances")...), map[string]any{
			"by": env.Actor, "event_id": env.ID, "submit_id": submitID, "ts": env.TS,
		})
		if note, ok := wpNonEmpty(env.Data["note"]); ok {
			wpAddNote(s, env, note)
		}
		touch(s, env)
	})

	r.Register("workpackage.waive", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		target, ok := wpULID(env.Data["target_id"])
		reason, okReason := wpNonEmpty(env.Data["reason"])
		if !ok || !okReason || target == env.Entity {
			return
		}
		s.Meta["waivers"] = append(append([]any{}, wpList(s.Meta, "waivers")...), map[string]any{
			"by": env.Actor, "event_id": env.ID, "reason": reason, "target_id": target, "ts": env.TS,
		})
		wpAddNote(s, env, reason)
		touch(s, env)
	})

	r.Register("workpackage.cancel", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		s.Meta["cancellations"] = append(append([]any{}, wpList(s.Meta, "cancellations")...), map[string]any{
			"by": env.Actor, "event_id": env.ID, "ts": env.TS,
		})
		if reason, ok := wpNonEmpty(env.Data["reason"]); ok {
			wpAddNote(s, env, reason)
		}
		touch(s, env)
	})

	r.Register("workpackage.note", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		body, ok := wpNonEmpty(env.Data["body"])
		if !ok {
			return
		}
		wpAddNote(s, env, body)
		touch(s, env)
	})

	r.Register("workpackage.link", func(s *State, env *event.Envelope) {
		if !created(s) {
			return
		}
		rel, id, ok := wpLinkValid(env.Data["rel"], env.Data["id"], env.Entity)
		if !ok {
			return
		}
		remove, _ := env.Data["remove"].(bool)
		links := append([]any{}, wpList(s.Meta, "links")...)
		idx := wpHasLink(links, rel, id)
		switch {
		case remove && idx >= 0:
			links = append(links[:idx], links[idx+1:]...)
		case !remove && idx < 0:
			links = append(links, map[string]any{"id": id, "rel": rel})
			wpSortLinks(links)
		default:
			return
		}
		s.Meta["links"] = links
		touch(s, env)
	})

	return r
}
