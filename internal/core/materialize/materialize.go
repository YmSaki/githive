// Package materialize implements fold: the pure function that turns an
// event set into an entity's current state (docs/02-data-model.md
// 「イベントの全順序と実体化」). It is organized as a kind -> reducer
// registry so that adding a feature means registering reducers, not
// changing the fold engine itself (docs/13-roadmap.md P0 作業分解).
//
// Determinism (docs/02-data-model.md 決定性の不変条件, docs/14-testing.md
// 順序不変性) rests on two rules enforced here, uniformly for every kind:
//  1. events are folded in ID (ULID) order, regardless of input order;
//  2. any kind ending in ".checkpoint" is always skipped
//     (docs/03-sync-and-concurrency.md「チェックポイント」).
package materialize

import (
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
)

// State is the generic accumulator threaded through a fold. Meta is nil
// until some reducer creates it (i.e. the entity does not exist yet).
// Collections holds append-only keyed data (comments, notes, ...): each
// named collection maps event ID -> item. Scratch holds working data a
// feature's reducers need between events but that isn't part of the public
// meta/collections shape (e.g. mutable label/assignee sets).
type State struct {
	Meta        map[string]any
	Collections map[string]map[string]any
	Scratch     map[string]any
}

// NewState returns an empty, not-yet-created state.
func NewState() *State {
	return &State{
		Collections: map[string]map[string]any{},
		Scratch:     map[string]any{},
	}
}

// Reducer applies one event's effect to state. Reducers must be pure
// functions of (state, event) - no I/O, no wall-clock reads
// (docs/01-architecture.md「依存規則」: materialize is a pure function).
type Reducer func(s *State, env *event.Envelope)

// Registry maps a fully-qualified kind (e.g. "issue.status") to the reducer
// that applies it.
type Registry struct {
	reducers map[string]Reducer
	// scratchDecoder rebuilds this feature's typed Scratch values when a
	// fold is restarted from a checkpoint snapshot (ADR-0016). Nil for
	// features whose reducers never touch Scratch.
	scratchDecoder ScratchDecoder
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{reducers: map[string]Reducer{}}
}

// Register installs the reducer for kind, overwriting any previous one.
func (r *Registry) Register(kind string, fn Reducer) {
	r.reducers[kind] = fn
}

// Fold folds events (in any order) into a state, per the rules in the
// package doc. Unknown kinds are ignored (forward compatibility,
// docs/02-data-model.md「読み手は未知フィールドを無視し」の kind 版).
func (r *Registry) Fold(events []*event.Envelope) *State {
	return r.FoldFrom(nil, events)
}

// FoldFrom is Fold starting from an already-computed state instead of an
// empty one, so that a reader which restored a checkpoint snapshot only has
// to apply the events after it (docs/03-sync-and-concurrency.md
// 「チェックポイント」, ADR-0016). A nil initial means "start empty", making
// Fold the special case.
//
// The caller must guarantee that every event in events sorts after every
// event already folded into initial; otherwise the result differs from
// folding the whole set, breaking determinism. chain.FindCheckpointStart
// plus the checkpoint's max_event_id establish that (ADR-0016 条件 3).
// FoldFrom takes ownership of initial and mutates it in place.
func (r *Registry) FoldFrom(initial *State, events []*event.Envelope) *State {
	s := initial
	if s == nil {
		s = NewState()
	}
	ordered := event.SortEvents(events)
	for _, env := range ordered {
		if strings.HasSuffix(env.Kind, ".checkpoint") {
			continue
		}
		fn, ok := r.reducers[env.Kind]
		if !ok {
			continue
		}
		fn(s, env)
	}
	return s
}
