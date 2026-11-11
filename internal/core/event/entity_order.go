package event

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEventOrderViolation means an entity's event set would silently discard
// pre-create edits or let a distinct create replace its original identity.
var ErrEventOrderViolation = errors.New("event: event_order_violation")

// ValidateEntityOrder checks admission safety for entity and users key histories.
// It does not change reducer ordering: create must be unique and sort before
// every mutation of that entity. Checkpoints are outside the logical history.
// Sets with no create are left to existing missing-entity checks. Callers
// must first enforce same-ID canonical identity (IDSet), so collisions retain
// their distinct error and identical event copies may be collapsed.
func ValidateEntityOrder(events []*Envelope) error {
	type group struct {
		create string
		first  string
	}
	groups := map[string]*group{}
	for _, env := range events {
		feature, operation, ok := strings.Cut(env.Kind, ".")
		if !ok || (feature != "issue" && feature != "task" && feature != "chat") || operation == "checkpoint" {
			continue
		}
		key := feature + "/" + env.Entity
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		if g.first == "" || env.ID < g.first {
			g.first = env.ID
		}
		if operation == "create" {
			if g.create != "" && g.create != env.ID {
				return fmt.Errorf("%w: %s has distinct create events %s and %s; inspect the original history before repairing it", ErrEventOrderViolation, key, g.create, env.ID)
			}
			g.create = env.ID
		}
	}
	for key, g := range groups {
		if g.create != "" && g.first < g.create {
			return fmt.Errorf("%w: %s event %s sorts before create %s and would be ignored; inspect peer clocks and the original history", ErrEventOrderViolation, key, g.first, g.create)
		}
	}
	return validateUsersKeyOrder(events)
}
