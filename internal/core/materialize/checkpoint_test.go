package materialize

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

// checkpointRoundTrip renders s with StateToMap, pushes it through the
// canonical JSON encoder/decoder the way a real checkpoint event would
// travel, and restores it with r.StateFromMap. Going through the encoder is
// the point: it is where a Scratch value with no JSON shape would be caught,
// and where numbers get normalized to json.Number.
func checkpointRoundTrip(t *testing.T, r *Registry, s *State) *State {
	t.Helper()
	m, err := StateToMap(s)
	if err != nil {
		t.Fatalf("StateToMap: %v", err)
	}
	raw, err := event.Encode(m)
	if err != nil {
		t.Fatalf("encode checkpoint state: %v", err)
	}
	decoded, err := event.DecodeGeneric(raw)
	if err != nil {
		t.Fatalf("decode checkpoint state: %v", err)
	}
	restored, err := r.StateFromMap(decoded.(map[string]any))
	if err != nil {
		t.Fatalf("StateFromMap: %v", err)
	}
	return restored
}

// TestCheckpointShortcutMatchesFullFold is A3's central property: for every
// feature, folding "checkpoint snapshot + the events after it" must produce
// exactly the same state as folding every event from scratch, down to the
// byte in canonical JSON (docs/02-data-model.md「決定性の不変条件」,
// ADR-0016). The split point is swept across the event list so that every
// reducer gets exercised on both sides of it.
func TestCheckpointShortcutMatchesFullFold(t *testing.T) {
	for _, f := range checkpointFeatures() {
		t.Run(f.name, func(t *testing.T) {
			for seed := int64(1); seed <= 5; seed++ {
				events := f.events(rand.New(rand.NewSource(seed)))
				ordered := event.SortEvents(events)
				full := f.registry.Fold(ordered)
				want := foldSignature(t, full)

				for split := 0; split <= len(ordered); split++ {
					snapshot := f.registry.Fold(ordered[:split])
					restored := checkpointRoundTrip(t, f.registry, snapshot)
					shortcut := f.registry.FoldFrom(restored, ordered[split:])
					if got := foldSignature(t, shortcut); got != want {
						t.Fatalf("seed %d split %d: shortcut fold differs from full fold\n--- shortcut ---\n%s\n--- full ---\n%s",
							seed, split, got, want)
					}
				}
			}
		})
	}
}

// TestCheckpointRoundTripPreservesScratch spells out the failure mode
// ADR-0016 was written for: the Scratch half of the fold state (notify acks,
// users policy, issue labels, chat participants) is invisible in
// meta/collections, so a snapshot that dropped it would restore a
// plausible-looking but wrong state.
func TestCheckpointRoundTripPreservesScratch(t *testing.T) {
	t.Run("notify acks", func(t *testing.T) {
		events := notifyCheckpointEvents(rand.New(rand.NewSource(7)))
		full := NotifyRegistry.Fold(events)
		restored := checkpointRoundTrip(t, NotifyRegistry, full)
		before, after := NotifyAcks(full), NotifyAcks(restored)
		if len(before) == 0 {
			t.Fatal("fixture produced no acks; the test would prove nothing")
		}
		if len(before) != len(after) {
			t.Fatalf("acks lost in round-trip: %v -> %v", before, after)
		}
		for k, want := range before {
			got := after[k]
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("acks[%q] = %v, want %v", k, got, want)
			}
		}
	})

	t.Run("users policy", func(t *testing.T) {
		events := usersCheckpointEvents(rand.New(rand.NewSource(7)))
		full := UsersRegistry.Fold(events)
		restored := checkpointRoundTrip(t, UsersRegistry, full)
		if Policy(full) == nil {
			t.Fatal("fixture produced no policy; the test would prove nothing")
		}
		if Policy(restored) == nil {
			t.Fatal("policy lost in round-trip")
		}
		if full.Meta != nil {
			t.Fatal("fixture assumption broken: users state should have a nil Meta")
		}
		if restored.Meta != nil {
			t.Error("nil Meta came back as a non-nil object; users would look created")
		}
	})

	t.Run("issue labels and assignees", func(t *testing.T) {
		events := issueCheckpointEvents(rand.New(rand.NewSource(7)))
		full := IssueRegistry.Fold(events)
		restored := checkpointRoundTrip(t, IssueRegistry, full)
		for _, key := range []string{"labels", "assignees"} {
			before, _ := full.Scratch[key].(map[string]bool)
			after, ok := restored.Scratch[key].(map[string]bool)
			if !ok {
				t.Fatalf("scratch %q did not restore as map[string]bool: %T", key, restored.Scratch[key])
			}
			if len(before) == 0 {
				t.Fatalf("fixture produced no %s; the test would prove nothing", key)
			}
			for k, v := range before {
				if after[k] != v {
					t.Errorf("scratch %s[%q] = %v, want %v", key, k, after[k], v)
				}
			}
		}
	})
}

// TestStateFromMapRejectsOldFormat covers the fallback path: a checkpoint
// written before ADR-0016 has no scratch key, and must be refused rather
// than restored into a state missing half its data.
func TestStateFromMapRejectsOldFormat(t *testing.T) {
	old := map[string]any{
		"meta":        map[string]any{"id": "01j8x0a2b3c4d5e6f7g8h9j0ka", "status": "open"},
		"collections": map[string]any{"comments": map[string]any{}},
	}
	_, err := IssueRegistry.StateFromMap(old)
	if !errors.Is(err, ErrCheckpointStateUnsupported) {
		t.Fatalf("expected ErrCheckpointStateUnsupported, got %v", err)
	}
}

// TestStateFromMapRejectsUnknownScratchKey keeps a future reducer from
// half-restoring: a scratch key this build does not understand invalidates
// the shortcut instead of being dropped.
func TestStateFromMapRejectsUnknownScratchKey(t *testing.T) {
	m := map[string]any{
		"meta":        map[string]any{},
		"collections": map[string]any{},
		"scratch":     map[string]any{"something_new": map[string]any{}},
	}
	_, err := IssueRegistry.StateFromMap(m)
	if !errors.Is(err, ErrCheckpointStateUnsupported) {
		t.Fatalf("expected ErrCheckpointStateUnsupported, got %v", err)
	}
}

// TestFoldFromNilIsFold pins the equivalence Fold relies on.
func TestFoldFromNilIsFold(t *testing.T) {
	events := issueCheckpointEvents(rand.New(rand.NewSource(3)))
	a := foldSignature(t, IssueRegistry.Fold(events))
	b := foldSignature(t, IssueRegistry.FoldFrom(nil, events))
	if a != b {
		t.Errorf("FoldFrom(nil, …) != Fold(…)\n%s\n---\n%s", a, b)
	}
}
