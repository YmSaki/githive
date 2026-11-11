package event

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestStrictJSONSharedVectors(t *testing.T) {
	raw, err := os.ReadFile(specDir(t, "vectors/json-decoding") + "/01-strict-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Source, Hex string
			Accepted          bool
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			input := []byte(tc.Source)
			if tc.Hex != "" {
				var err error
				input, err = hex.DecodeString(tc.Hex)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := DecodeGeneric(input)
			if (err == nil) != tc.Accepted {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, tc.Accepted, err)
			}
		})
	}
}

func TestCanonicalEncodingRejectsInvalidStringsAndExcessiveDepth(t *testing.T) {
	nested := any(0)
	for range MaxJSONDepth + 1 {
		nested = []any{nested}
	}
	cases := []any{string([]byte{0xff}), map[string]any{string([]byte{0xfe}): 1}, []string{string([]byte{0xff})}, nested}
	for _, input := range cases {
		if _, err := Encode(input); err == nil {
			t.Errorf("Encode accepted invalid value")
		}
		if _, err := EncodeCompactLine(input); err == nil {
			t.Errorf("EncodeCompactLine accepted invalid value")
		}
	}
}

func limitTestEnvelope() *Envelope {
	return &Envelope{V: SchemaVersion, Kind: "issue.comment", ID: "01j8xq4d3nbz9k7w2m5e8h1t6a", TS: "2026-07-04T12:34:56.789Z", Actor: "tester@example.com", Entity: "01j8x0a2b3c4d5e6f7g8h9j0ka", Data: map[string]any{"body": ""}}
}

func TestEventJSONSizeBoundaryIncludesCanonicalBytes(t *testing.T) {
	env := limitTestEnvelope()
	raw, err := env.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	env.Data["body"] = strings.Repeat("x", MaxEventBytes-len(raw))
	raw, err = env.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != MaxEventBytes {
		t.Fatalf("fixture bytes=%d", len(raw))
	}
	if _, err := Decode(raw); err != nil {
		t.Fatalf("boundary decode: %v", err)
	}
	env.Data["body"] = env.Data["body"].(string) + "x"
	if _, err := env.CanonicalJSON(); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversize write: %v", err)
	}
	// Encoding directly bypasses the envelope writer, simulating foreign input.
	over, err := Encode(env.ToMap())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(over); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversize read: %v", err)
	}
	compact, err := EncodeCompactLine(env.ToMap())
	if err != nil {
		t.Fatal(err)
	}
	if len(compact) >= MaxEventBytes {
		t.Fatal("compact fixture must fit raw budget")
	}
	if _, err := Decode([]byte(compact)); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("canonical expansion bypassed limit: %v", err)
	}
}

func TestFromMapRejectsFractionalVersion(t *testing.T) {
	for _, v := range []any{1.9, 1e100, json.Number("1.9"), json.Number("9223372036854775808")} {
		m := limitTestEnvelope().ToMap()
		m["v"] = v
		if _, err := FromMap(m); err == nil {
			t.Fatalf("accepted version %v", v)
		}
	}
}
