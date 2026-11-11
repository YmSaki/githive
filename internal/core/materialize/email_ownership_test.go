package materialize

import "testing"

func TestEmailOwnershipAllowsAliasesButNeverCombinesUsers(t *testing.T) {
	s := NewState()
	s.Collections["users"] = map[string]any{
		"alice": map[string]any{"emails": []any{"primary@example.com", "alias@example.com", "alias@example.com"}, "roles": []any{"admin"}, "keys": []any{map[string]any{"pub": "alice-key", "revoked_at": nil}}},
	}
	for _, e := range []string{"primary@example.com", "alias@example.com"} {
		if !IsAdmin(s, e) || len(ActiveKeysForEmail(s, e)) != 1 {
			t.Fatalf("valid alias lost: %s", e)
		}
	}
	if c := EmailConflicts(s); len(c) != 0 {
		t.Fatalf("sameuser aliases conflict: %+v", c)
	}
	s.Collections["users"]["robot"] = map[string]any{"emails": []any{"primary@example.com"}, "keys": []any{map[string]any{"pub": "robot-key", "revoked_at": nil}}}
	if IsAdmin(s, "primary@example.com") || len(ActiveKeysForEmail(s, "primary@example.com")) != 0 {
		t.Fatal("ambiguous email combines privileges or keys")
	}
	for _, entry := range AllowedSigners(s) {
		if entry.Email == "primary@example.com" {
			t.Fatal("ambiguous principal emitted")
		}
	}
	if !IsAdmin(s, "alias@example.com") {
		t.Fatal("unambiguous alias denied")
	}
	c := EmailConflicts(s)
	if len(c) != 1 || c[0].Email != "primary@example.com" || len(c[0].Usernames) != 2 || c[0].Usernames[0] != "alice" || c[0].Usernames[1] != "robot" {
		t.Fatalf("conflicts=%+v", c)
	}
}
