package event

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedDataSchemaPreservesValidExtensionsAndRejectsMalformed(t *testing.T) {
	for i, tc := range []struct {
		kind  string
		data  map[string]any
		valid bool
	}{
		{"issue.create", map[string]any{"title": "valid", "future": map[string]any{"x": true}}, true},
		{"issue.edit", map[string]any{}, true},
		{"chat.post", map[string]any{}, false},
		{"issue.edit", map[string]any{"title": 123}, false},
		{"issue.status", map[string]any{"to": "bogus"}, false},
		{"task.edit", map[string]any{"due": "2026-99-99"}, true},
		{"notify.post", map[string]any{"title": "valid", "targets": []any{"all"}, "source": map[string]any{"kind": "opaque future source", "id": "01j8xq4d3nbz9k7w2m5e8h1t61"}}, true},
		{"meta.init", map[string]any{"schema_version": json.Number("1.00000000000000000001"), "project": "p", "features": []any{}, "created_at": "2026-11-11T00:00:00.000Z"}, false},
		{"meta.init", map[string]any{"schema_version": json.Number("1.0"), "project": "p", "features": []any{}, "created_at": "2026-11-11T00:00:00.000Z"}, true},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.kind, i), func(t *testing.T) {
			known, err := ValidateDataSchema(&Envelope{Kind: tc.kind, Data: tc.data})
			if !known || (err == nil) != tc.valid {
				t.Fatalf("known=%v err=%v valid=%v", known, err, tc.valid)
			}
		})
	}
	if known, err := ValidateDataSchema(&Envelope{Kind: "issue.future", Data: map[string]any{"anything": 123}}); known || err != nil {
		t.Fatalf("unknownkind changed compatibility: %v %v", known, err)
	}
}

func TestEmbeddedDataSchemasAcceptAllPositiveFoldVectors(t *testing.T) {
	paths, err := filepath.Glob("../../../spec/vectors/fold-*/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("vectors: %v %d", err, len(paths))
	}
	count := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeGeneric(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range decoded.(map[string]any)["events"].([]any) {
			env, err := FromMap(item.(map[string]any))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if known, err := ValidateDataSchema(env); !known || err != nil {
				t.Fatalf("%s/%s: known=%v err=%v", path, env.Kind, known, err)
			}
			count++
		}
	}
	if count < 50 {
		t.Fatalf("unexpectedly few emitted/vector events: %d", count)
	}
}

func TestEmbeddedDataSchemaHandlesAdversarialNumbersAndCycles(t *testing.T) {
	if _, err := ValidateDataSchema(nil); err == nil {
		t.Fatal("nil envelope accepted")
	}
	for _, number := range []json.Number{"1.000000000000000000001", "1e-999999999", "1e-9999999999999999999999999", "1e999999999"} {
		env := &Envelope{Kind: "meta.init", Data: map[string]any{"schema_version": number, "project": "p", "features": []any{}, "created_at": "2026-11-11T00:00:00.000Z"}}
		if _, err := ValidateDataSchema(env); err == nil {
			t.Fatalf("invalid number accepted: %s", number)
		}
	}
	data := map[string]any{"title": "valid"}
	data["cycle"] = data
	if _, err := ValidateDataSchema(&Envelope{Kind: "issue.create", Data: data}); err == nil {
		t.Fatal("cyclic input accepted")
	}
}
