package materialize

import (
	"math/rand"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func notifyEvent(id, kind string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: kind, ID: id, TS: "2026-07-04T00:00:00.000Z",
		Actor: "a@example.com", Entity: id, Data: data, Extra: map[string]any{},
	}
}

func TestNotifyPostBuckets(t *testing.T) {
	events := []*event.Envelope{
		{
			V: 1, Kind: "notify.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t61", TS: "2026-07-04T00:00:00.000Z",
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t61",
			Data: map[string]any{"targets": []any{"user:b@example.com"}, "title": "t1"}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t62", TS: "2026-08-01T00:00:00.000Z",
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t62",
			Data: map[string]any{"targets": []any{"all"}, "title": "t2"}, Extra: map[string]any{},
		},
	}
	state := NotifyRegistry.Fold(events)
	if len(state.Collections["posts"]) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(state.Collections["posts"]))
	}
	p1 := state.Collections["posts"]["01j8xq4d3nbz9k7w2m5e8h1t61"].(map[string]any)
	if p1["month"] != "2026-07" {
		t.Errorf("expected month 2026-07, got %v", p1["month"])
	}
	p2 := state.Collections["posts"]["01j8xq4d3nbz9k7w2m5e8h1t62"].(map[string]any)
	if p2["month"] != "2026-08" {
		t.Errorf("expected month 2026-08, got %v", p2["month"])
	}
}

func TestNotifyAckDedupeAndSort(t *testing.T) {
	postID := "01j8xq4d3nbz9k7w2m5e8h1t61"
	events := []*event.Envelope{
		{
			V: 1, Kind: "notify.post", ID: postID, TS: "2026-07-04T00:00:00.000Z",
			Actor: "a@example.com", Entity: postID,
			Data: map[string]any{"targets": []any{"all"}, "title": "t"}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.ack", ID: "01j8xq4d3nbz9k7w2m5e8h1t62", TS: "2026-07-04T00:01:00.000Z",
			Actor: "z@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t62",
			Data: map[string]any{"ack_of": postID}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.ack", ID: "01j8xq4d3nbz9k7w2m5e8h1t63", TS: "2026-07-04T00:02:00.000Z",
			Actor: "b@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t63",
			Data: map[string]any{"ack_of": postID}, Extra: map[string]any{},
		},
		{
			// Duplicate ack from the same actor must not double up.
			V: 1, Kind: "notify.ack", ID: "01j8xq4d3nbz9k7w2m5e8h1t64", TS: "2026-07-04T00:03:00.000Z",
			Actor: "z@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t64",
			Data: map[string]any{"ack_of": postID}, Extra: map[string]any{},
		},
	}
	state := NotifyRegistry.Fold(events)
	acks := NotifyAcks(state)
	got := acks[postID]
	want := []string{"b@example.com", "z@example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestNotifyCheckpointIgnored(t *testing.T) {
	events := []*event.Envelope{
		notifyEvent("01j8xq4d3nbz9k7w2m5e8h1t61", "notify.checkpoint", map[string]any{
			"state": map[string]any{}, "count": 0, "hash": "x",
		}),
	}
	state := NotifyRegistry.Fold(events)
	if state.Meta != nil {
		t.Errorf("checkpoint-only fold should leave Meta nil, got %+v", state.Meta)
	}
}

// notifySignature includes acks (which live outside the generic
// Meta/Collections shape, see NotifyAcks) alongside the usual state
// signature, so order-invariance tests catch divergence there too.
func notifySignature(t *testing.T, s *State) string {
	t.Helper()
	acks := NotifyAcks(s)
	acksAny := make(map[string]any, len(acks))
	for k, v := range acks {
		acksAny[k] = v
	}
	base := canonicalStateSignature(t, s)
	extra := canonicalOf(t, acksAny)
	return base + "|acks=" + extra
}

// TestNotifyFoldOrderInvariance checks docs/02-data-model.md's determinism
// invariant for the notify fold, including month bucketing and ack
// dedupe/sort (docs/14-testing.md「順序不変性」).
func TestNotifyFoldOrderInvariance(t *testing.T) {
	postA := "01j8xq4d3nbz9k7w2m5e8h1t61"
	postB := "01j8xq4d3nbz9k7w2m5e8h1t62"
	events := []*event.Envelope{
		{
			V: 1, Kind: "notify.post", ID: postA, TS: "2026-07-04T00:00:00.000Z",
			Actor: "a@example.com", Entity: postA,
			Data: map[string]any{"targets": []any{"all"}, "title": "t1"}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.post", ID: postB, TS: "2026-08-01T00:00:00.000Z",
			Actor: "a@example.com", Entity: postB,
			Data: map[string]any{"targets": []any{"user:b@example.com"}, "title": "t2"}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.ack", ID: "01j8xq4d3nbz9k7w2m5e8h1t63", TS: "2026-07-04T00:01:00.000Z",
			Actor: "z@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t63",
			Data: map[string]any{"ack_of": postA}, Extra: map[string]any{},
		},
		{
			V: 1, Kind: "notify.ack", ID: "01j8xq4d3nbz9k7w2m5e8h1t64", TS: "2026-07-04T00:02:00.000Z",
			Actor: "b@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t64",
			Data: map[string]any{"ack_of": postA}, Extra: map[string]any{},
		},
	}
	want := notifySignature(t, NotifyRegistry.Fold(events))

	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 10; trial++ {
		shuffled := append([]*event.Envelope(nil), events...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := notifySignature(t, NotifyRegistry.Fold(shuffled))
		if got != want {
			t.Fatalf("trial %d: notify fold is order-dependent\nwant: %s\ngot:  %s", trial, want, got)
		}
	}
}
