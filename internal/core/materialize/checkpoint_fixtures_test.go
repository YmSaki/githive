package materialize

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

// The fixtures below build event sequences that touch every reducer of a
// feature - crucially including the ones that write to State.Scratch, since
// Scratch is the part of the fold state a checkpoint snapshot used to drop
// (ADR-0016). They are deliberately hand-built rather than sampled from
// spec/vectors: the property test needs long, randomized sequences it can
// split at every position.

const cpBase32 = "0123456789abcdefghjkmnpqrstvwxyz"

// cpID returns the n-th of a monotonically increasing series of valid
// ULIDs, so that a fixture's slice order is also its fold order.
func cpID(n int) string {
	if n < 0 || n >= 32*32*32 {
		panic(fmt.Sprintf("cpID: n out of range: %d", n))
	}
	id := []byte("01k2c00000000000000000000")
	suffix := []byte{
		cpBase32[(n>>10)&31],
		cpBase32[(n>>5)&31],
		cpBase32[n&31],
	}
	return string(id[:23]) + string(suffix)
}

const cpEntity = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func cpTS(n int) string {
	return fmt.Sprintf("2026-07-28T09:%02d:%02d.000Z", n/60%60, n%60)
}

func cpEvent(n int, kind, actor string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V: event.SchemaVersion, Kind: kind, ID: cpID(n), TS: cpTS(n),
		Actor: actor, Entity: cpEntity, Data: data, Extra: map[string]any{},
	}
}

func cpActor(r *rand.Rand) string {
	return []string{"a@example.com", "b@example.com", "c@example.com"}[r.Intn(3)]
}

func pick(r *rand.Rand, options ...string) string { return options[r.Intn(len(options))] }

type checkpointFeature struct {
	name     string
	registry *Registry
	events   func(*rand.Rand) []*event.Envelope
}

func checkpointFeatures() []checkpointFeature {
	return []checkpointFeature{
		{"issue", IssueRegistry, issueCheckpointEvents},
		{"task", TaskRegistry, taskCheckpointEvents},
		{"chat", ChatRegistry, chatCheckpointEvents},
		{"notify", NotifyRegistry, notifyCheckpointEvents},
		{"users", UsersRegistry, usersCheckpointEvents},
	}
}

func issueCheckpointEvents(r *rand.Rand) []*event.Envelope {
	events := []*event.Envelope{
		cpEvent(0, "issue.create", "a@example.com", map[string]any{"title": "seed", "body": "b"}),
	}
	// Fixed transitions keep the status machine in legal territory; the
	// randomness is in the label/assignee/link churn that drives Scratch.
	statuses := []string{"in_progress", "resolved", "closed"}
	for n := 1; n <= 18; n++ {
		actor := cpActor(r)
		switch r.Intn(6) {
		case 0:
			events = append(events, cpEvent(n, "issue.comment", actor, map[string]any{"body": fmt.Sprintf("c%d", n)}))
		case 1:
			events = append(events, cpEvent(n, "issue.label", actor, map[string]any{
				"add":    []any{pick(r, "bug", "docs", "perf")},
				"remove": []any{pick(r, "bug", "docs", "perf")},
			}))
		case 2:
			events = append(events, cpEvent(n, "issue.assign", actor, map[string]any{
				"add":    []any{pick(r, "a@example.com", "b@example.com")},
				"remove": []any{pick(r, "b@example.com", "c@example.com")},
			}))
		case 3:
			events = append(events, cpEvent(n, "issue.link", actor, map[string]any{
				"rel":    pick(r, "task", "chat"),
				"id":     cpID(500 + r.Intn(3)),
				"remove": r.Intn(2) == 0,
			}))
		case 4:
			events = append(events, cpEvent(n, "issue.edit", actor, map[string]any{"title": fmt.Sprintf("t%d", n)}))
		default:
			events = append(events, cpEvent(n, "issue.status", actor, map[string]any{"to": statuses[r.Intn(len(statuses))]}))
		}
	}
	return events
}

func taskCheckpointEvents(r *rand.Rand) []*event.Envelope {
	events := []*event.Envelope{
		cpEvent(0, "task.create", "a@example.com", map[string]any{"title": "seed", "owner": "a@example.com"}),
	}
	for n := 1; n <= 18; n++ {
		actor := cpActor(r)
		switch r.Intn(5) {
		case 0:
			events = append(events, cpEvent(n, "task.note", actor, map[string]any{"body": fmt.Sprintf("n%d", n)}))
		case 1:
			events = append(events, cpEvent(n, "task.reassign", actor, map[string]any{"owner": cpActor(r)}))
		case 2:
			events = append(events, cpEvent(n, "task.link", actor, map[string]any{
				"rel":    pick(r, "issue", "chat"),
				"id":     cpID(500 + r.Intn(3)),
				"remove": r.Intn(2) == 0,
			}))
		case 3:
			events = append(events, cpEvent(n, "task.edit", actor, map[string]any{"priority": pick(r, "low", "high")}))
		default:
			// Exercises status_history, which lives in Scratch.
			events = append(events, cpEvent(n, "task.status", actor, map[string]any{
				"to": pick(r, "doing", "review", "blocked", "todo", "done", "cancelled"),
			}))
		}
	}
	return events
}

func chatCheckpointEvents(r *rand.Rand) []*event.Envelope {
	events := []*event.Envelope{
		cpEvent(0, "chat.create", "a@example.com", map[string]any{"title": "seed", "body": "first"}),
	}
	for n := 1; n <= 15; n++ {
		actor := cpActor(r)
		switch r.Intn(4) {
		case 0:
			events = append(events, cpEvent(n, "chat.edit_meta", actor, map[string]any{"title": fmt.Sprintf("t%d", n)}))
		case 1:
			// supersedes drives the message-replacement rule.
			events = append(events, cpEvent(n, "chat.post", actor, map[string]any{
				"body":       fmt.Sprintf("m%d", n),
				"supersedes": cpID(r.Intn(n)),
			}))
		default:
			// Participants (Scratch) grow with each distinct actor.
			events = append(events, cpEvent(n, "chat.post", actor, map[string]any{"body": fmt.Sprintf("m%d", n)}))
		}
	}
	return events
}

func notifyCheckpointEvents(r *rand.Rand) []*event.Envelope {
	var events []*event.Envelope
	var postIDs []string
	for n := 0; n <= 15; n++ {
		actor := cpActor(r)
		if len(postIDs) > 0 && r.Intn(3) == 0 {
			// acks live entirely in Scratch.
			events = append(events, cpEvent(n, "notify.ack", actor, map[string]any{
				"ack_of": postIDs[r.Intn(len(postIDs))],
			}))
			continue
		}
		events = append(events, cpEvent(n, "notify.post", actor, map[string]any{
			"to":    []any{pick(r, "user:a@example.com", "group:reviewers")},
			"title": fmt.Sprintf("p%d", n),
			"body":  "b",
		}))
		postIDs = append(postIDs, cpID(n))
	}
	return events
}

func usersCheckpointEvents(r *rand.Rand) []*event.Envelope {
	events := []*event.Envelope{
		cpEvent(0, "users.user_set", "a@example.com", map[string]any{
			"username": "alice",
			"fields":   map[string]any{"emails": []any{"a@example.com"}, "roles": []any{"admin"}},
		}),
	}
	for n := 1; n <= 15; n++ {
		actor := cpActor(r)
		switch r.Intn(5) {
		case 0:
			events = append(events, cpEvent(n, "users.key_add", actor, map[string]any{
				"username": pick(r, "alice", "bob"),
				"pub":      fmt.Sprintf("ssh-ed25519 AAAA%d comment", n),
			}))
		case 1:
			events = append(events, cpEvent(n, "users.group_set", actor, map[string]any{
				"name":    pick(r, "reviewers", "owners"),
				"members": []any{"alice", "bob"},
			}))
		case 2:
			events = append(events, cpEvent(n, "users.group_remove", actor, map[string]any{
				"name": pick(r, "reviewers", "owners"),
			}))
		case 3:
			// policy lives in Scratch and is replaced wholesale.
			events = append(events, cpEvent(n, "users.policy_set", actor, map[string]any{
				"rules":   []any{map[string]any{"ref": "refs/projects/*", "allow": pick(r, "write", "read")}},
				"default": pick(r, "deny", "allow"),
			}))
		default:
			events = append(events, cpEvent(n, "users.user_set", actor, map[string]any{
				"username": pick(r, "alice", "bob"),
				"fields":   map[string]any{"display": fmt.Sprintf("d%d", n)},
			}))
		}
	}
	return events
}

// foldSignature is canonicalStateSignature extended with Scratch, which is
// exactly what this task's property test must compare: two folds that agree
// on meta and collections but differ in Scratch would still render different
// trees for notify (acks.json) and users (policy.json).
func foldSignature(t *testing.T, s *State) string {
	t.Helper()
	m, err := StateToMap(s)
	if err != nil {
		t.Fatalf("StateToMap: %v", err)
	}
	return canonicalOf(t, m)
}
