// Package merge implements event-union merging: the mechanism by which two
// diverged commit chains converge without manual conflict resolution
// (docs/03-sync-and-concurrency.md「event-union マージ」, ADR-0004).
//
// Because fold (internal/core/materialize) is a pure function of an event
// set, merging is simply: union the two sides' event sets (by ID, so
// duplicates from shared ancestry collapse for free) and fold the result.
// No common-ancestor search is needed - shared history just contributes the
// same events on both sides, which the union already dedupes.
package merge

import (
	"sort"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// UnionEvents merges any number of event sets into one, deduplicated by ID.
// Order of the input sets does not affect the result (commutative,
// associative, idempotent - docs/14-testing.md「マージの収束」).
func UnionEvents(sets ...[]*event.Envelope) []*event.Envelope {
	byID := map[string]*event.Envelope{}
	var order []string
	for _, set := range sets {
		for _, env := range set {
			if _, seen := byID[env.ID]; !seen {
				order = append(order, env.ID)
			}
			byID[env.ID] = env
		}
	}
	sort.Strings(order)
	out := make([]*event.Envelope, len(order))
	for i, id := range order {
		out[i] = byID[id]
	}
	return out
}

// Fold unions the given event sets and folds them with registry. This is
// the pure, git-independent core of a merge.
func Fold(registry *materialize.Registry, sets ...[]*event.Envelope) *materialize.State {
	return registry.Fold(UnionEvents(sets...))
}
