package materialize

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func specDir(t *testing.T, sub string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "spec", sub)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate spec/%s above %s", sub, dir)
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Dummy-kind determinism property test for the fold engine itself, independent
// of any real feature (docs/13-roadmap.md「ダミー kind で決定性プロパティテ
// スト」, docs/14-testing.md「順序不変性」).
// ---------------------------------------------------------------------------

// newDummyRegistry registers a trivial last-write-wins counter kind plus a
// checkpoint kind, purely to exercise the generic engine's order-invariance
// and checkpoint-skip rules.
func newDummyRegistry() *Registry {
	r := NewRegistry()
	r.Register("dummy.set", func(s *State, env *event.Envelope) {
		if s.Meta == nil {
			s.Meta = map[string]any{}
		}
		s.Meta["value"] = env.Data["value"]
		s.Meta["last_event"] = env.ID
	})
	return r
}

func dummyEnvelope(id string, value string) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: "dummy.set", ID: id, TS: "2026-07-04T00:00:00.000Z",
		Actor: "tester@example.com", Entity: "01j8x0a2b3c4d5e6f7g8h9j0ka",
		Data: map[string]any{"value": value}, Extra: map[string]any{},
	}
}

func dummyCheckpoint(id string) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: "dummy.checkpoint", ID: id, TS: "2026-07-04T00:00:00.000Z",
		Actor: "tester@example.com", Entity: "01j8x0a2b3c4d5e6f7g8h9j0ka",
		Data: map[string]any{"bogus_state": "should never be read"}, Extra: map[string]any{},
	}
}

func TestFoldOrderInvariance(t *testing.T) {
	r := newDummyRegistry()
	ids := []string{
		"01j8xq4d3nbz9k7w2m5e8h1t61",
		"01j8xq4d3nbz9k7w2m5e8h1t62",
		"01j8xq4d3nbz9k7w2m5e8h1t63",
		"01j8xq4d3nbz9k7w2m5e8h1t64",
		"01j8xq4d3nbz9k7w2m5e8h1t65",
	}
	events := make([]*event.Envelope, len(ids))
	for i, id := range ids {
		events[i] = dummyEnvelope(id, fmt.Sprintf("v%d", i))
	}
	// The winner under last-write-wins-by-ID is always the max ID, regardless
	// of the order events are handed to Fold in.
	want := r.Fold(events).Meta["last_event"]

	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 20; trial++ {
		shuffled := append([]*event.Envelope(nil), events...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := r.Fold(shuffled).Meta["last_event"]
		if got != want {
			t.Fatalf("trial %d: order dependence detected: got %v want %v", trial, got, want)
		}
	}
}

func TestFoldCheckpointTransparency(t *testing.T) {
	r := newDummyRegistry()
	base := []*event.Envelope{
		dummyEnvelope("01j8xq4d3nbz9k7w2m5e8h1t61", "a"),
		dummyEnvelope("01j8xq4d3nbz9k7w2m5e8h1t62", "b"),
	}
	withCheckpoint := append(append([]*event.Envelope(nil), base...), dummyCheckpoint("01j8xq4d3nbz9k7w2m5e8h1t63"))

	stateWithout := r.Fold(base)
	stateWith := r.Fold(withCheckpoint)
	if stateWithout.Meta["value"] != stateWith.Meta["value"] {
		t.Errorf("checkpoint changed fold result: without=%v with=%v", stateWithout.Meta, stateWith.Meta)
	}
}

// ---------------------------------------------------------------------------
// Golden vectors: issue and task fold rules must match
// spec/vectors/fold-issue and spec/vectors/fold-task exactly (structurally),
// including under shuffled input (spec/README.md「Go 実装への要求」).
// ---------------------------------------------------------------------------

type foldVector struct {
	Events           []json.RawMessage `json:"events"`
	ExpectedMeta     json.RawMessage   `json:"expected_meta"`
	ExpectedComments json.RawMessage   `json:"expected_comments"`
	ExpectedNotes    json.RawMessage   `json:"expected_notes"`
}

func canonicalOf(t *testing.T, v any) string {
	t.Helper()
	s, err := event.EncodeString(v)
	if err != nil {
		t.Fatalf("canonical encode: %v", err)
	}
	return s
}

func metaAsAny(meta map[string]any) any {
	if meta == nil {
		return nil
	}
	return map[string]any(meta)
}

func collectionAsAny(coll map[string]any) any {
	return map[string]any(coll)
}

func runFoldVectorDir(t *testing.T, sub string, registry *Registry, collectionName string, expectedCollection func(foldVector) json.RawMessage) {
	dir := specDir(t, sub)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("no vectors found in %s", sub)
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var vec foldVector
			if err := json.Unmarshal(raw, &vec); err != nil {
				t.Fatal(err)
			}

			events := make([]*event.Envelope, len(vec.Events))
			for i, evRaw := range vec.Events {
				env, err := event.Decode(evRaw)
				if err != nil {
					t.Fatalf("event[%d]: %v", i, err)
				}
				events[i] = env
			}

			expectedMetaVal, err := event.DecodeGeneric(vec.ExpectedMeta)
			if err != nil {
				t.Fatal(err)
			}
			expectedCollRaw := expectedCollection(vec)
			expectedCollVal, err := event.DecodeGeneric(expectedCollRaw)
			if err != nil {
				t.Fatal(err)
			}
			wantMeta := canonicalOf(t, expectedMetaVal)
			wantColl := canonicalOf(t, expectedCollVal)

			check := func(t *testing.T, events []*event.Envelope, label string) {
				state := registry.Fold(events)
				gotMeta := canonicalOf(t, metaAsAny(state.Meta))
				if gotMeta != wantMeta {
					t.Errorf("%s: meta mismatch\n want: %s\n got:  %s", label, wantMeta, gotMeta)
				}
				gotColl := canonicalOf(t, collectionAsAny(state.Collections[collectionName]))
				if gotColl != wantColl {
					t.Errorf("%s: %s mismatch\n want: %s\n got:  %s", label, collectionName, wantColl, gotColl)
				}
			}

			check(t, events, "original order")

			rng := rand.New(rand.NewSource(int64(len(name)) + 7))
			for trial := 0; trial < 3; trial++ {
				shuffled := append([]*event.Envelope(nil), events...)
				rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
				check(t, shuffled, fmt.Sprintf("shuffle trial %d", trial))
			}
		})
	}
}

func TestFoldIssueVectors(t *testing.T) {
	runFoldVectorDir(t, "vectors/fold-issue", IssueRegistry, "comments", func(v foldVector) json.RawMessage {
		return v.ExpectedComments
	})
}

func TestFoldTaskVectors(t *testing.T) {
	runFoldVectorDir(t, "vectors/fold-task", TaskRegistry, "notes", func(v foldVector) json.RawMessage {
		return v.ExpectedNotes
	})
}
