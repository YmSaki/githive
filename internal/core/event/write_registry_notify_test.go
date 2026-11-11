package event

import (
	"errors"
	"testing"
)

func TestRegistryAndNotifyWriteBoundaries(t *testing.T) {
	fullID := "01j8xq4d3nbz9k7w2m5e8h1t61"
	cases := []struct {
		kind string
		data map[string]any
	}{
		{"users.user_set", map[string]any{"username": "alice", "fields": map[string]any{"kind": "robot"}}},
		{"users.user_set", map[string]any{"username": "alice", "fields": map[string]any{"status": "gone"}}},
		{"users.key_add", map[string]any{"username": "../admin"}},
		{"users.group_set", map[string]any{"name": "..", "members": []string{"alice"}}},
		{"users.group_set", map[string]any{"name": "team", "members": []string{"../alice"}}},
		{"notify.post", map[string]any{"title": "test", "targets": []string{"alice"}}},
		{"notify.post", map[string]any{"title": "test", "targets": []any{7}}},
		{"notify.post", map[string]any{"title": "test", "targets": []string{}}},
		{"notify.post", map[string]any{"title": "test", "targets": []string{"group:../team"}}},
		{"notify.post", map[string]any{"title": "test", "targets": []string{"all"}, "source": map[string]any{"kind": "issue", "id": fullID[:8]}}},
	}
	for _, tc := range cases {
		err := ValidateWriteData(&Envelope{Kind: tc.kind, Data: tc.data})
		var invalid *InvalidInputError
		if !errors.As(err, &invalid) {
			t.Errorf("%s %v: want invalid input, got %v", tc.kind, tc.data, err)
		}
	}
	for _, e := range []*Envelope{
		{Kind: "notify.post", Data: map[string]any{"title": "valid", "targets": []string{"user:alice@example.com", "group:team", "all"}, "source": map[string]any{"kind": "custom-feature", "id": fullID}}},
		{Kind: "users.user_set", Data: map[string]any{"username": "alice", "fields": map[string]any{"kind": "agent", "status": "suspended"}}},
	} {
		if err := ValidateWriteData(e); err != nil {
			t.Errorf("schema-valid data rejected: %v", err)
		}
	}
	// The stored-name boundary is deliberately narrower than new-write rules.
	legacy := &Envelope{Kind: "users.user_set", Data: map[string]any{"username": "alice", "fields": map[string]any{"kind": "robot", "status": "gone"}}}
	if err := ValidateStoredData(legacy); err != nil {
		t.Fatalf("safe legacy fields were rejected at the read boundary: %v", err)
	}
}
