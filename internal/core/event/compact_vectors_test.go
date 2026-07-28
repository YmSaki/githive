package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCompactVectors mirrors TestCanonicalVectors but exercises
// EncodeCompactLine against spec/vectors/canonical-compact/*.json: the
// 圧縮形 golden vectors for docs/02-data-model.md's "canonical JSON" 唯一の
// 例外 (notify's events/<yyyy-mm>.jsonl). Every vector here must byte-match
// both this Go encoder and spec/reference/canonical_json.py's
// encode_compact_line (spec/validate.py's run_canonical_compact_validation).
func TestCompactVectors(t *testing.T) {
	dir := specDir(t, "vectors/canonical-compact")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no canonical-compact vectors found")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var vec canonicalVector
			if err := json.Unmarshal(raw, &vec); err != nil {
				t.Fatal(err)
			}
			// Decode with UseNumber via DecodeGeneric so numeric literals
			// round-trip exactly like the Python reference implementation.
			input, err := DecodeGeneric(vec.Input)
			if err != nil {
				t.Fatal(err)
			}

			actual, err := EncodeCompactLine(input)
			if err != nil {
				t.Fatalf("encode error: %v", err)
			}
			if actual != vec.Expected {
				t.Errorf("mismatch\n expected: %q\n actual:   %q", vec.Expected, actual)
			}
		})
	}
}
