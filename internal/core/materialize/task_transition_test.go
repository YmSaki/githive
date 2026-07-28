package materialize

import (
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

const taskTransitionEntity = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func taskStatusEvent(id, to string) *event.Envelope {
	return &event.Envelope{
		V: event.SchemaVersion, Kind: "task.status", ID: id,
		TS:    "2026-07-28T09:00:00.000Z",
		Actor: "tester@example.com", Entity: taskTransitionEntity,
		Data:  map[string]any{"to": to},
		Extra: map[string]any{},
	}
}

func taskCreateEvent(id string) *event.Envelope {
	return &event.Envelope{
		V: event.SchemaVersion, Kind: "task.create", ID: id,
		TS:    "2026-07-28T09:00:00.000Z",
		Actor: "tester@example.com", Entity: taskTransitionEntity,
		Data:  map[string]any{"title": "t"},
		Extra: map[string]any{},
	}
}

// TestTaskCancelFromTerminalIsIgnored is the unit-level statement of
// ADR-0015: once a task is done or cancelled, a cancel event is an invalid
// transition, handled exactly like every other invalid transition - silently
// dropped, with status, updated_at and status_history all left untouched.
func TestTaskCancelFromTerminalIsIgnored(t *testing.T) {
	cases := []struct {
		name  string
		reach []string // transitions applied before the cancel attempt
	}{
		{"from done", []string{"doing", "review", "done"}},
		{"from cancelled", []string{"cancelled"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := []*event.Envelope{taskCreateEvent("01k1b2c3d4e5f6g7h8j9k0m100")}
			for i, to := range c.reach {
				events = append(events, taskStatusEvent(nthID(i+1), to))
			}
			before := TaskRegistry.Fold(events)
			wantStatus := c.reach[len(c.reach)-1]
			if got := before.Meta["status"]; got != wantStatus {
				t.Fatalf("setup: status = %v, want %s", got, wantStatus)
			}
			wantHistory := len(before.Meta["status_history"].([]any))
			wantUpdated := before.Meta["updated_at"]

			after := TaskRegistry.Fold(append(events, taskStatusEvent(nthID(len(c.reach)+1), "cancelled")))
			if got := after.Meta["status"]; got != wantStatus {
				t.Errorf("status = %v, want it unchanged at %s", got, wantStatus)
			}
			if got := len(after.Meta["status_history"].([]any)); got != wantHistory {
				t.Errorf("status_history grew to %d entries, want %d", got, wantHistory)
			}
			if got := after.Meta["updated_at"]; got != wantUpdated {
				t.Errorf("updated_at = %v, want it unchanged at %v", got, wantUpdated)
			}
		})
	}
}

// TestTaskCancelFromNonTerminalStillWorks guards the other side of the
// change: cancelling an in-flight task is still allowed.
func TestTaskCancelFromNonTerminalStillWorks(t *testing.T) {
	for i, from := range []string{"todo", "doing", "review", "blocked"} {
		events := []*event.Envelope{taskCreateEvent("01k1b2c3d4e5f6g7h8j9k0m100")}
		switch from {
		case "doing", "review", "blocked":
			events = append(events, taskStatusEvent(nthID(1), "doing"))
		}
		switch from {
		case "review":
			events = append(events, taskStatusEvent(nthID(2), "review"))
		case "blocked":
			events = append(events, taskStatusEvent(nthID(2), "blocked"))
		}
		events = append(events, taskStatusEvent(nthID(3+i), "cancelled"))

		state := TaskRegistry.Fold(events)
		if got := state.Meta["status"]; got != "cancelled" {
			t.Errorf("from %s: status = %v, want cancelled", from, got)
		}
	}
}

// TestTaskResumeFromTerminalStillWorks: docs/features/task.md keeps 再開 via
// todo available from a terminal state; ADR-0015 only forbade cancelling.
func TestTaskResumeFromTerminalStillWorks(t *testing.T) {
	events := []*event.Envelope{
		taskCreateEvent("01k1b2c3d4e5f6g7h8j9k0m100"),
		taskStatusEvent(nthID(1), "cancelled"),
		taskStatusEvent(nthID(2), "todo"),
	}
	state := TaskRegistry.Fold(events)
	if got := state.Meta["status"]; got != "todo" {
		t.Errorf("status = %v, want todo (再開)", got)
	}
}

// nthID returns a monotonically increasing valid ULID so that fold's ID
// ordering matches the order events are appended in these tests.
func nthID(n int) string {
	const base = "01k1b2c3d4e5f6g7h8j9k0m1"
	const digits = "0123456789abcdefghjkmnpqrstvwxyz"
	return base + string(digits[n/32%32]) + string(digits[n%32])
}
