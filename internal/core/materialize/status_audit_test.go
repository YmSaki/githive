package materialize

import (
	"github.com/ymsaki/githive/internal/core/event"
	"testing"
)

func TestIgnoredStatusAuditReusesRulesAndDeduplicatesCopies(t *testing.T) {
	makeEvent := func(id, kind string, data map[string]any) *event.Envelope {
		return &event.Envelope{V: 2, ID: "01j8xq4d3nbz9k7w2m5e8h1t" + id, Kind: kind, Entity: "01j8xq4d3nbz9k7w2m5e8h1t60", TS: "2026-11-11T00:00:00.000Z", Actor: "a@example.com", Data: data}
	}
	for _, feature := range []string{"issue", "task"} {
		valid, bad := "in_progress", "archived"
		if feature == "task" {
			valid, bad = "doing", "done"
		}
		create := makeEvent("60", feature+".create", map[string]any{"title": "valid"})
		ignored := makeEvent("61", feature+".status", map[string]any{"to": bad})
		accepted := makeEvent("62", feature+".status", map[string]any{"to": valid})
		events := []*event.Envelope{accepted, create, ignored, accepted}
		warnings := IgnoredStatusTransitions(feature, events)
		if len(warnings) != 1 || warnings[0].EventID != ignored.ID || warnings[0].To != bad {
			t.Fatalf("%s audit=%+v", feature, warnings)
		}
	}
}
