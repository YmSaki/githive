package materialize

import (
	"math/rand"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func usersEvent(id, kind string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: kind, ID: id, TS: "2026-07-04T00:00:00.000Z",
		Actor: "admin@example.com", Entity: id, Data: data, Extra: map[string]any{},
	}
}

func TestUsersUserSetCreatesAndUpdates(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.user_set", map[string]any{
			"username": "yuumiya",
			"fields": map[string]any{
				"display": "ゆうみや",
				"emails":  []any{"staroprog1103@gmail.com"},
				"roles":   []any{"admin"},
			},
		}),
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t62", "users.user_set", map[string]any{
			"username": "yuumiya",
			"fields":   map[string]any{"status": "suspended"},
		}),
	}
	state := UsersRegistry.Fold(events)
	user, ok := state.Collections["users"]["yuumiya"].(map[string]any)
	if !ok {
		t.Fatal("expected user yuumiya to exist")
	}
	if user["display"] != "ゆうみや" {
		t.Errorf("expected display to survive LWW update of unrelated field, got %v", user["display"])
	}
	if user["status"] != "suspended" {
		t.Errorf("expected status to be updated, got %v", user["status"])
	}
}

func TestUsersKeyAddAndRevoke(t *testing.T) {
	pub := "ssh-ed25519 AAAA yuumiya@main"
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.user_set", map[string]any{
			"username": "yuumiya", "fields": map[string]any{"emails": []any{"a@example.com"}},
		}),
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t62", "users.key_add", map[string]any{
			"username": "yuumiya", "pub": pub,
		}),
	}
	state := UsersRegistry.Fold(events)
	user := state.Collections["users"]["yuumiya"].(map[string]any)
	keys := asMapSlice(user["keys"])
	if len(keys) != 1 || keys[0]["pub"] != pub || keys[0]["revoked_at"] != nil {
		t.Fatalf("unexpected keys after add: %+v", keys)
	}

	// Duplicate add is a no-op.
	dup := append(events, usersEvent("01j8xq4d3nbz9k7w2m5e8h1t63", "users.key_add", map[string]any{
		"username": "yuumiya", "pub": pub,
	}))
	state2 := UsersRegistry.Fold(dup)
	if len(asMapSlice(state2.Collections["users"]["yuumiya"].(map[string]any)["keys"])) != 1 {
		t.Error("expected duplicate key_add to be a no-op")
	}

	revoked := append(dup, usersEvent("01j8xq4d3nbz9k7w2m5e8h1t64", "users.key_revoke", map[string]any{
		"username": "yuumiya", "pub": pub,
	}))
	state3 := UsersRegistry.Fold(revoked)
	keys3 := asMapSlice(state3.Collections["users"]["yuumiya"].(map[string]any)["keys"])
	if len(keys3) != 1 || keys3[0]["revoked_at"] != "2026-07-04T00:00:00.000Z" {
		t.Errorf("expected key to be revoked, got %+v", keys3)
	}

	if got := ActiveKeysForEmail(state3, "a@example.com"); len(got) != 0 {
		t.Errorf("expected no active keys after revoke, got %v", got)
	}
	if got := ActiveKeysForEmail(state, "a@example.com"); len(got) != 1 || got[0] != pub {
		t.Errorf("expected 1 active key before revoke, got %v", got)
	}
}

func TestUsersKeyOpsIgnoredForUnknownUser(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.key_add", map[string]any{
			"username": "ghost", "pub": "ssh-ed25519 AAAA",
		}),
	}
	state := UsersRegistry.Fold(events)
	if _, ok := state.Collections["users"]["ghost"]; ok {
		t.Error("expected key_add on unknown user to be ignored, not auto-create")
	}
}

func TestUsersGroupSetAndRemove(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.group_set", map[string]any{
			"name": "core", "members": []any{"yuumiya", "dev-agent-01"}, "description": "コア開発",
		}),
	}
	state := UsersRegistry.Fold(events)
	group, ok := state.Collections["groups"]["core"].(map[string]any)
	if !ok {
		t.Fatal("expected group core to exist")
	}
	if group["description"] != "コア開発" {
		t.Errorf("unexpected description: %v", group["description"])
	}

	removed := append(events, usersEvent("01j8xq4d3nbz9k7w2m5e8h1t62", "users.group_remove", map[string]any{"name": "core"}))
	state2 := UsersRegistry.Fold(removed)
	if _, ok := state2.Collections["groups"]["core"]; ok {
		t.Error("expected group to be removed")
	}
}

func TestUsersPolicySetIsWholeObjectReplace(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.policy_set", map[string]any{
			"rules": []any{
				map[string]any{"refs": "refs/projects/**", "allow": []any{"role:member"}, "actions": []any{"push"}},
			},
			"default": "deny",
		}),
	}
	state := UsersRegistry.Fold(events)
	policy := Policy(state)
	if policy == nil || policy["default"] != "deny" {
		t.Fatalf("unexpected policy: %+v", policy)
	}

	replaced := append(events, usersEvent("01j8xq4d3nbz9k7w2m5e8h1t62", "users.policy_set", map[string]any{
		"rules": []any{}, "default": "allow",
	}))
	state2 := UsersRegistry.Fold(replaced)
	policy2 := Policy(state2)
	if policy2["default"] != "allow" {
		t.Errorf("expected policy to be replaced wholesale, got %+v", policy2)
	}
	rules, _ := policy2["rules"].([]any)
	if len(rules) != 0 {
		t.Errorf("expected old rules to be gone after whole-object replace, got %v", rules)
	}
}

func TestUsersIsAdmin(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.user_set", map[string]any{
			"username": "yuumiya",
			"fields": map[string]any{
				"emails": []any{"a@example.com"},
				"roles":  []any{"admin"},
			},
		}),
	}
	state := UsersRegistry.Fold(events)
	if !IsAdmin(state, "a@example.com") {
		t.Error("expected a@example.com to be admin")
	}
	if IsAdmin(state, "b@example.com") {
		t.Error("expected unknown email to not be admin")
	}
}

func TestUsersCheckpointIgnored(t *testing.T) {
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.checkpoint", map[string]any{
			"state": map[string]any{}, "count": 0, "hash": "x",
		}),
	}
	state := UsersRegistry.Fold(events)
	if len(state.Collections["users"]) != 0 {
		t.Errorf("checkpoint should not create anything, got %+v", state.Collections)
	}
}

func TestUsersFoldOrderInvariance(t *testing.T) {
	pub := "ssh-ed25519 AAAA yuumiya@main"
	events := []*event.Envelope{
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "users.user_set", map[string]any{
			"username": "yuumiya", "fields": map[string]any{"emails": []any{"a@example.com"}, "roles": []any{"admin"}},
		}),
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t62", "users.key_add", map[string]any{"username": "yuumiya", "pub": pub}),
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t63", "users.group_set", map[string]any{"name": "core", "members": []any{"yuumiya"}}),
		usersEvent("01j8xq4d3nbz9k7w2m5e8h1t64", "users.policy_set", map[string]any{"rules": []any{}, "default": "deny"}),
	}
	want := usersSignature(t, UsersRegistry.Fold(events))

	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 10; trial++ {
		shuffled := append([]*event.Envelope(nil), events...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := usersSignature(t, UsersRegistry.Fold(shuffled))
		if got != want {
			t.Fatalf("trial %d: users fold is order-dependent\nwant: %s\ngot:  %s", trial, want, got)
		}
	}
}

func usersSignature(t *testing.T, s *State) string {
	t.Helper()
	base := canonicalStateSignature(t, s)
	policy := canonicalOf(t, Policy(s))
	return base + "|policy=" + policy
}
