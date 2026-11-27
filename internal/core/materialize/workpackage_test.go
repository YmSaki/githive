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

// workpackageCheckpointEvents drives every workpackage reducer, including
// in-place claim updates, so the checkpoint round-trip covers them.
func workpackageCheckpointEvents(r *rand.Rand) []*event.Envelope {
	events := []*event.Envelope{
		cpEvent(0, "workpackage.create", "a@example.com", map[string]any{"title": "seed", "body": "b", "depends_on": []any{cpID(90)}}),
	}
	claimID := ""
	for n := 1; n <= 20; n++ {
		actor := cpActor(r)
		var e *event.Envelope
		switch r.Intn(8) {
		case 0:
			e = cpEvent(n, "workpackage.edit", actor, map[string]any{"body": pick(r, "x", "x\n", "y")})
		case 1:
			e = cpEvent(n, "workpackage.claim", actor, map[string]any{"lease_until": "2030-01-01T00:00:00.000Z"})
			claimID = cpID(n)
		case 2:
			if claimID == "" {
				continue
			}
			e = cpEvent(n, "workpackage.release", actor, map[string]any{"claim_id": claimID, "note": "r"})
		case 3:
			e = cpEvent(n, "workpackage.submit", actor, map[string]any{"commit": nil, "acceptance_version": cpID(0), "reviewers": []any{"b@example.com"}})
		case 4:
			e = cpEvent(n, "workpackage.accept", actor, map[string]any{"submit_id": cpID(n - 1)})
		case 5:
			e = cpEvent(n, "workpackage.note", actor, map[string]any{"body": pick(r, "x", "x\n")})
		case 6:
			e = cpEvent(n, "workpackage.link", actor, map[string]any{"rel": pick(r, "issue", "task"), "id": cpID(91), "remove": r.Intn(2) == 0})
		default:
			e = cpEvent(n, "workpackage.dependency", actor, map[string]any{"target": cpID(92), "remove": r.Intn(2) == 0})
		}
		events = append(events, e)
	}
	return events
}

type wpFoldVector struct {
	Events        []json.RawMessage `json:"events"`
	ExpectedMeta  json.RawMessage   `json:"expected_meta"`
	ExpectedBody  string            `json:"expected_body"`
	ExpectedNotes json.RawMessage   `json:"expected_notes"`
}

// TestFoldWorkpackageVectors compares Go's fold with spec/vectors/
// fold-workpackage (meta, body and notes), in original and shuffled order.
func TestFoldWorkpackageVectors(t *testing.T) {
	dir := specDir(t, "vectors/fold-workpackage")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no vectors")
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var vec wpFoldVector
			if err := json.Unmarshal(raw, &vec); err != nil {
				t.Fatal(err)
			}
			var events []*event.Envelope
			for _, r := range vec.Events {
				env, err := event.Decode(r)
				if err != nil {
					t.Fatal(err)
				}
				events = append(events, env)
			}
			var wantMeta, wantNotes any
			if err := json.Unmarshal(vec.ExpectedMeta, &wantMeta); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(vec.ExpectedNotes, &wantNotes); err != nil {
				t.Fatal(err)
			}
			rng := rand.New(rand.NewSource(int64(len(name))))
			for trial := 0; trial < 4; trial++ {
				input := append([]*event.Envelope(nil), events...)
				if trial > 0 {
					rng.Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
				}
				s := WorkpackageRegistry.Fold(input)
				label := fmt.Sprintf("trial %d", trial)
				if got, want := canonicalOf(t, metaAsAny(s.Meta)), canonicalOf(t, wantMeta); got != want {
					t.Fatalf("%s meta:\n got %s\nwant %s", label, got, want)
				}
				if got := s.Collections["body"]["text"]; got != vec.ExpectedBody {
					t.Fatalf("%s body: got %q want %q", label, got, vec.ExpectedBody)
				}
				if got, want := canonicalOf(t, collectionAsAny(s.Collections["notes"])), canonicalOf(t, wantNotes); got != want {
					t.Fatalf("%s notes:\n got %s\nwant %s", label, got, want)
				}
				if len(s.Scratch) != 0 {
					t.Fatalf("%s scratch must stay empty, got %v", label, s.Scratch)
				}
			}
		})
	}
}

func TestNormalizeLease(t *testing.T) {
	ts := "2026-07-04T10:00:00.000Z"
	for _, c := range []struct{ in, want string }{
		{"2026-07-04T19:30:00.123999+09:00", "2026-07-04T10:30:00.123Z"},
		{"2026-07-01T00:00:00Z", ts},
		{"2027-01-01T00:00:00Z", "2026-07-11T10:00:00.000Z"},
	} {
		got, ok := NormalizeLease(c.in, ts)
		if !ok || got != c.want {
			t.Errorf("NormalizeLease(%q) = %q,%v want %q", c.in, got, ok, c.want)
		}
	}
	// Grammar shared with spec/reference/fold_workpackage.py.
	for _, bad := range []string{"tomorrow", "2026-07-05T10:00:00,5Z", "2026-07-05T10:00:00.1234567891Z",
		"2026-07-05T10:00:00+24:00", "2026-07-05T10:00:00+23:60", "2026-07-05t10:00:00z", "2026-07-05T10:00:00", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00+01:00", "2026-07-05T10:00:00Z\n", "٢٠٢٦-07-05T10:00:00Z"} {
		if _, ok := NormalizeLease(bad, ts); ok {
			t.Errorf("NormalizeLease(%q) accepted", bad)
		}
	}
}

// TestWorkpackageCheckpointRoundTrip checks that the checkpoint data.state
// is exactly {meta, collections{body,notes}, scratch:{}} and restores.
func TestWorkpackageCheckpointRoundTrip(t *testing.T) {
	events := workpackageCheckpointEvents(rand.New(rand.NewSource(3)))
	s := WorkpackageRegistry.Fold(events)
	m, err := StateToMap(s)
	if err != nil {
		t.Fatal(err)
	}
	if sc := m["scratch"].(map[string]any); len(sc) != 0 {
		t.Fatalf("scratch = %v", sc)
	}
	coll := m["collections"].(map[string]any)
	if _, ok := coll["body"].(map[string]any)["text"].(string); !ok {
		t.Fatal("collections.body.text missing")
	}
	encoded, err := event.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := event.DecodeGeneric(encoded)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := WorkpackageRegistry.StateFromMap(decoded.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	more := []*event.Envelope{
		cpEvent(40, "workpackage.edit", "a@example.com", map[string]any{"body": "after checkpoint"}),
		cpEvent(41, "workpackage.note", "a@example.com", map[string]any{"body": "n"}),
	}
	viaShortcut := WorkpackageRegistry.FoldFrom(restored, more)
	full := WorkpackageRegistry.Fold(append(append([]*event.Envelope(nil), events...), more...))
	if canonicalStateSignature(t, viaShortcut) != canonicalStateSignature(t, full) {
		t.Fatal("checkpoint shortcut and full fold differ")
	}
}
