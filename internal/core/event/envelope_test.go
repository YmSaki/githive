package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type foldVector struct {
	Events []json.RawMessage `json:"events"`
}

// TestEnvelopeVectorsValidate exercises Decode/Validate against every real
// event envelope in the fold-issue/fold-task golden vectors (mirrors the
// envelope-schema half of spec/validate.py's run_schema_validation).
func TestEnvelopeVectorsValidate(t *testing.T) {
	for _, sub := range []string{"vectors/fold-issue", "vectors/fold-task"} {
		dir := specDir(t, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var vec foldVector
			if err := json.Unmarshal(raw, &vec); err != nil {
				t.Fatal(err)
			}
			for i, evRaw := range vec.Events {
				env, err := Decode(evRaw)
				if err != nil {
					t.Errorf("%s: event[%d]: %v", entry.Name(), i, err)
					continue
				}
				if env.V < MinSchemaVersion || env.V > SchemaVersion {
					t.Errorf("%s: event[%d]: v=%d outside the accepted range %d-%d", entry.Name(), i, env.V, MinSchemaVersion, SchemaVersion)
				}
			}
		}
	}
}

func TestValidateRejectsBadEnvelopes(t *testing.T) {
	base := func() *Envelope {
		return &Envelope{
			V:      1,
			Kind:   "issue.create",
			ID:     "01j8xq4d3nbz9k7w2m5e8h1t6a",
			TS:     "2026-07-04T12:34:56.789Z",
			Actor:  "yuumiya@example.com",
			Entity: "01j8x0a2b3c4d5e6f7g8h9j0ka",
			Data:   map[string]any{},
		}
	}

	cases := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{"future version", func(e *Envelope) { e.V = SchemaVersion + 1 }},
		{"zero version", func(e *Envelope) { e.V = 0 }},
		{"bad kind", func(e *Envelope) { e.Kind = "IssueCreate" }},
		{"bad id", func(e *Envelope) { e.ID = "not-a-ulid" }},
		{"bad ts", func(e *Envelope) { e.TS = "2026-07-04" }},
		{"actor no at", func(e *Envelope) { e.Actor = "nobody" }},
		{"actor whitespace", func(e *Envelope) { e.Actor = "a b@example.com" }},
		{"bad entity", func(e *Envelope) { e.Entity = "short" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			c.mutate(e)
			if err := e.Validate(); err == nil {
				t.Errorf("expected validation error, got nil")
			}
		})
	}

	if err := base().Validate(); err != nil {
		t.Errorf("expected valid envelope, got error: %v", err)
	}
}

func TestCanonicalJSONRoundTrip(t *testing.T) {
	e := &Envelope{
		V:      1,
		Kind:   "issue.comment",
		ID:     "01j8xq4d3nbz9k7w2m5e8h1t6a",
		TS:     "2026-07-04T12:34:56.789Z",
		Actor:  "yuumiya@example.com",
		Entity: "01j8x0a2b3c4d5e6f7g8h9j0ka",
		Data:   map[string]any{"body": "LGTM"},
		Extra:  map[string]any{},
	}
	out, err := e.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(out)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v\n%s", err, out)
	}
	if decoded.Kind != e.Kind || decoded.ID != e.ID || decoded.Data["body"] != "LGTM" {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}

	out2, err := decoded.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(out2) {
		t.Errorf("canonical JSON is not stable across round-trip:\n%s\n---\n%s", out, out2)
	}
}

// TestOrderingVectors exercises the ULID lexical-order vectors
// (spec/vectors/ordering) against the production ordering path,
// event.SortEvents, which is what materialize.Registry.Fold actually calls
// to realize state = fold(sort_by_event_id(events)) (docs/02-data-model.md).
// Each raw ULID is wrapped in a minimal Envelope so this exercises the same
// function real fold code paths use, rather than a parallel sort.Strings
// call that could silently drift from SortEvents's behavior.
func TestOrderingVectors(t *testing.T) {
	dir := specDir(t, "vectors/ordering")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no ordering vectors found")
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var vec struct {
				Input          []string `json:"input"`
				ExpectedSorted []string `json:"expected_sorted"`
			}
			if err := json.Unmarshal(raw, &vec); err != nil {
				t.Fatal(err)
			}
			envs := make([]*Envelope, len(vec.Input))
			for i, id := range vec.Input {
				envs[i] = &Envelope{ID: id}
			}
			sorted := SortEvents(envs)
			got := make([]string, len(sorted))
			for i, e := range sorted {
				got[i] = e.ID
			}
			if len(got) != len(vec.ExpectedSorted) {
				t.Fatalf("length mismatch: got %v want %v", got, vec.ExpectedSorted)
			}
			for i := range got {
				if got[i] != vec.ExpectedSorted[i] {
					t.Errorf("index %d: got %q want %q", i, got[i], vec.ExpectedSorted[i])
				}
			}
		})
	}
}
