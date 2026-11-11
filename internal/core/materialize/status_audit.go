package materialize

import "github.com/ymsaki/githive/internal/core/event"

// IgnoredStatusTransition is a valid-shaped status request the reducer ignores.
type IgnoredStatusTransition struct{ EventID, Kind, From, To string }

// IgnoredStatusTransitions audits the actual reducer predicates without changing
// the fold result. Callers first validate event identity, ref context and data.
// Identical physical event copies are logical set members, not extra transitions.
func IgnoredStatusTransitions(feature string, events []*event.Envelope) []IgnoredStatusTransition {
	var registry *Registry
	switch feature {
	case "issue":
		registry = IssueRegistry
	case "task":
		registry = TaskRegistry
	default:
		return nil
	}
	state := NewState()
	seen := map[string]bool{}
	var out []IgnoredStatusTransition
	for _, env := range event.SortEvents(events) {
		if seen[env.ID] {
			continue
		}
		seen[env.ID] = true
		if env.Kind == feature+".status" {
			from, hasFrom := state.Meta["status"].(string)
			to, _ := env.Data["to"].(string)
			accepted := false
			if state.Meta != nil {
				if feature == "issue" {
					accepted = issueValidTransition(from, hasFrom, to)
				} else {
					accepted = taskValidTransition(from, hasFrom, to)
				}
			}
			if !accepted {
				out = append(out, IgnoredStatusTransition{EventID: env.ID, Kind: env.Kind, From: from, To: to})
			}
		}
		state = registry.FoldFrom(state, []*event.Envelope{env})
	}
	return out
}
