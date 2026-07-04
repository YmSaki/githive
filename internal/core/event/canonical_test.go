package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// specVectorsDir locates spec/vectors relative to this test file, walking up
// to the repo root so `go test ./...` works regardless of CWD.
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

type canonicalVector struct {
	Input    json.RawMessage `json:"input"`
	Expected string          `json:"expected"`
}

func TestCanonicalVectors(t *testing.T) {
	dir := specDir(t, "vectors/canonical")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no canonical vectors found")
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

			actual, err := EncodeString(input)
			if err != nil {
				t.Fatalf("encode error: %v", err)
			}
			if actual != vec.Expected {
				t.Errorf("mismatch\n expected: %q\n actual:   %q", vec.Expected, actual)
			}
		})
	}
}
