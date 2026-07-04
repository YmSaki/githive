package materialize

import (
	"math/rand"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func chatEvent(id, entity, kind string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: kind, ID: id, TS: "2026-07-04T00:00:00.000Z",
		Actor: "a@example.com", Entity: entity, Data: data, Extra: map[string]any{},
	}
}

const chatEntity = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func TestChatCreateWithBodyBecomesFirstMessage(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{
			"title": "リリース手順の相談", "body": "まず手順を書きます",
		}),
	}
	state := ChatRegistry.Fold(events)
	if state.Meta == nil {
		t.Fatal("expected meta to be set")
	}
	if state.Meta["status"] != "open" {
		t.Errorf("expected status open, got %v", state.Meta["status"])
	}
	if state.Meta["message_count"] != 1 {
		t.Errorf("expected message_count 1, got %v", state.Meta["message_count"])
	}
	msg, ok := state.Collections["messages"]["01j8xq4d3nbz9k7w2m5e8h1t61"].(map[string]any)
	if !ok {
		t.Fatalf("expected create event to double as a message, got %+v", state.Collections["messages"])
	}
	if msg["body"] != "まず手順を書きます" {
		t.Errorf("unexpected message body: %v", msg["body"])
	}
	participants, _ := state.Meta["participants"].([]any)
	if len(participants) != 1 || participants[0] != "a@example.com" {
		t.Errorf("unexpected participants: %v", participants)
	}
}

func TestChatPostAndParticipants(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "t"}),
		{
			V: 1, Kind: "chat.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t62", TS: "2026-07-04T00:01:00.000Z",
			Actor: "b@example.com", Entity: chatEntity, Data: map[string]any{"body": "hi"}, Extra: map[string]any{},
		},
	}
	state := ChatRegistry.Fold(events)
	if state.Meta["message_count"] != 1 {
		t.Errorf("expected message_count 1 (create had no body), got %v", state.Meta["message_count"])
	}
	participants, _ := state.Meta["participants"].([]any)
	if len(participants) != 2 {
		t.Errorf("expected 2 participants, got %v", participants)
	}
}

func TestChatPostSupersedes(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "t"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t62", chatEntity, "chat.post", map[string]any{"body": "typo"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t63", chatEntity, "chat.post", map[string]any{
			"body": "fixed", "supersedes": "01j8xq4d3nbz9k7w2m5e8h1t62",
		}),
	}
	state := ChatRegistry.Fold(events)
	prior, ok := state.Collections["messages"]["01j8xq4d3nbz9k7w2m5e8h1t62"].(map[string]any)
	if !ok {
		t.Fatal("expected prior message to exist")
	}
	if prior["superseded_by"] != "01j8xq4d3nbz9k7w2m5e8h1t63" {
		t.Errorf("expected superseded_by to be set, got %+v", prior)
	}
}

func TestChatEditMetaAndArchive(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "t"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t62", chatEntity, "chat.edit_meta", map[string]any{"status": "archived", "title": "t2"}),
	}
	state := ChatRegistry.Fold(events)
	if state.Meta["status"] != "archived" {
		t.Errorf("expected archived, got %v", state.Meta["status"])
	}
	if state.Meta["title"] != "t2" {
		t.Errorf("expected updated title, got %v", state.Meta["title"])
	}
}

func TestChatSecondCreateIgnored(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "first"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t62", chatEntity, "chat.create", map[string]any{"title": "second"}),
	}
	state := ChatRegistry.Fold(events)
	if state.Meta["title"] != "first" {
		t.Errorf("expected first create to win, got %v", state.Meta["title"])
	}
}

func TestChatCheckpointIgnored(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "t"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t62", chatEntity, "chat.checkpoint", map[string]any{
			"state": map[string]any{}, "count": 1, "hash": "deadbeef",
		}),
	}
	state := ChatRegistry.Fold(events)
	if len(state.Collections["messages"]) != 0 {
		t.Errorf("checkpoint must not affect fold result, got %+v", state.Collections["messages"])
	}
}

// TestChatFoldOrderInvariance checks docs/02-data-model.md's determinism
// invariant for the chat fold: the same event set must fold to the same
// result regardless of input order (docs/14-testing.md「順序不変性」).
func TestChatFoldOrderInvariance(t *testing.T) {
	events := []*event.Envelope{
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t61", chatEntity, "chat.create", map[string]any{"title": "t", "body": "first"}),
		{
			V: 1, Kind: "chat.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t62", TS: "2026-07-04T00:01:00.000Z",
			Actor: "b@example.com", Entity: chatEntity, Data: map[string]any{"body": "second"}, Extra: map[string]any{},
		},
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t63", chatEntity, "chat.post", map[string]any{"body": "third"}),
		chatEvent("01j8xq4d3nbz9k7w2m5e8h1t64", chatEntity, "chat.edit_meta", map[string]any{"status": "archived"}),
	}
	want := canonicalStateSignature(t, ChatRegistry.Fold(events))

	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 10; trial++ {
		shuffled := append([]*event.Envelope(nil), events...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := canonicalStateSignature(t, ChatRegistry.Fold(shuffled))
		if got != want {
			t.Fatalf("trial %d: chat fold is order-dependent\nwant: %s\ngot:  %s", trial, want, got)
		}
	}
}
