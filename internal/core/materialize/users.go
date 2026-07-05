package materialize

import (
	"sort"

	"github.com/ymsaki/githive/internal/core/event"
)

// UsersRegistry is the fold registry for the users feature
// (docs/features/users.md). Like notify/stream, users/registry is a
// singleton with no single "create" event: it holds three independent
// sub-resources (users, groups, policy) rather than one meta.json, so
// State.Meta is unused here. Users and groups live in Collections; policy
// (a whole-object LWW value, not a keyed collection) lives in Scratch and
// is exposed via Policy().
var UsersRegistry = newUsersRegistry()

func ensureUsersCollections(s *State) {
	if s.Collections["users"] == nil {
		s.Collections["users"] = map[string]any{}
	}
	if s.Collections["groups"] == nil {
		s.Collections["groups"] = map[string]any{}
	}
}

// Policy returns the current fold's policy.json value, or nil if
// users.policy_set has never been applied.
func Policy(s *State) map[string]any {
	p, _ := s.Scratch["policy"].(map[string]any)
	return p
}

func newUsersRegistry() *Registry {
	r := NewRegistry()

	r.Register("users.user_set", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		username, _ := env.Data["username"].(string)
		if username == "" {
			return
		}
		fields, _ := env.Data["fields"].(map[string]any)

		user, ok := s.Collections["users"][username].(map[string]any)
		if !ok {
			user = map[string]any{
				"username": username,
				"kind":     "human",
				"status":   "active",
				"emails":   []any{},
				"roles":    []any{},
				"keys":     []any{},
			}
		}
		for k, v := range fields {
			if k == "keys" || k == "username" {
				continue // keys are managed only via key_add/key_revoke
			}
			user[k] = v
		}
		user["username"] = username
		s.Collections["users"][username] = user
	})

	r.Register("users.key_add", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		username, _ := env.Data["username"].(string)
		pub, _ := env.Data["pub"].(string)
		if username == "" || pub == "" {
			return
		}
		user, ok := s.Collections["users"][username].(map[string]any)
		if !ok {
			return // key_add on a user that was never user_set is ignored
		}
		keys := asMapSlice(user["keys"])
		for _, k := range keys {
			if k["pub"] == pub {
				return // already present, no-op
			}
		}
		keys = append(keys, map[string]any{
			"pub":        pub,
			"added_at":   env.TS,
			"revoked_at": nil,
		})
		user["keys"] = keysToAny(keys)
	})

	r.Register("users.key_revoke", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		username, _ := env.Data["username"].(string)
		pub, _ := env.Data["pub"].(string)
		if username == "" || pub == "" {
			return
		}
		user, ok := s.Collections["users"][username].(map[string]any)
		if !ok {
			return
		}
		keys := asMapSlice(user["keys"])
		for _, k := range keys {
			if k["pub"] == pub && k["revoked_at"] == nil {
				k["revoked_at"] = env.TS
			}
		}
		user["keys"] = keysToAny(keys)
	})

	r.Register("users.group_set", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		name, _ := env.Data["name"].(string)
		if name == "" {
			return
		}
		group := map[string]any{
			"name":    name,
			"members": env.Data["members"],
		}
		if desc, ok := env.Data["description"]; ok {
			group["description"] = desc
		}
		s.Collections["groups"][name] = group
	})

	r.Register("users.group_remove", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		name, _ := env.Data["name"].(string)
		if name == "" {
			return
		}
		delete(s.Collections["groups"], name)
	})

	r.Register("users.policy_set", func(s *State, env *event.Envelope) {
		ensureUsersCollections(s)
		rules, _ := env.Data["rules"]
		def, _ := env.Data["default"]
		s.Scratch["policy"] = map[string]any{
			"rules":   rules,
			"default": def,
		}
	})

	return r
}

func asMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func keysToAny(keys []map[string]any) []any {
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out
}

// ActiveKeysForEmail returns every non-revoked SSH public key registered to
// a user whose emails[] contains email (docs/features/users.md「emails」,
// docs/11-security.md「SSH 署名」).
func ActiveKeysForEmail(s *State, email string) []string {
	var pubs []string
	usernames := make([]string, 0, len(s.Collections["users"]))
	for username := range s.Collections["users"] {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames) // deterministic iteration for callers that care
	for _, username := range usernames {
		user, ok := s.Collections["users"][username].(map[string]any)
		if !ok {
			continue
		}
		emails := asStringSlice(user["emails"])
		matches := false
		for _, e := range emails {
			if e == email {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		for _, k := range asMapSlice(user["keys"]) {
			if k["revoked_at"] == nil {
				if pub, ok := k["pub"].(string); ok {
					pubs = append(pubs, pub)
				}
			}
		}
	}
	return pubs
}

// IsAdmin reports whether the user with the given email currently has the
// "admin" role (docs/11-security.md「検証規則」4).
func IsAdmin(s *State, email string) bool {
	for _, raw := range s.Collections["users"] {
		user, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		emails := asStringSlice(user["emails"])
		hasEmail := false
		for _, e := range emails {
			if e == email {
				hasEmail = true
				break
			}
		}
		if !hasEmail {
			continue
		}
		for _, role := range asStringSlice(user["roles"]) {
			if role == "admin" {
				return true
			}
		}
	}
	return false
}
