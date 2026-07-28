package event

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func versionedEnvelopeJSON(v int, withData bool) string {
	data := ""
	if withData {
		data = `,"data":{"title":"t"}`
	}
	return `{"v":` + strconv.Itoa(v) + `,"kind":"task.create","id":"01j8xq4d3nbz9k7w2m5e8h1t6a",` +
		`"ts":"2026-07-04T12:34:56.789Z","actor":"yuumiya@example.com",` +
		`"entity":"01j8x0a2b3c4d5e6f7g8h9j0ka"` + data + `}`
}

// TestDecodeAcceptsKnownOlderVersions is the compatibility half of
// ADR-0015: raising SchemaVersion must not make existing chains
// unreadable, so every version from MinSchemaVersion up to the current one
// still decodes.
func TestDecodeAcceptsKnownOlderVersions(t *testing.T) {
	for v := MinSchemaVersion; v <= SchemaVersion; v++ {
		env, err := Decode([]byte(versionedEnvelopeJSON(v, true)))
		if err != nil {
			t.Fatalf("v=%d: %v", v, err)
		}
		if env.V != v {
			t.Errorf("v=%d: decoded as %d", v, env.V)
		}
	}
}

// TestDecodeRejectsUnknownNewerVersion is the other half: an envelope from
// a newer tool is refused outright rather than folded under semantics it
// was not written for (docs/01-architecture.md「バージョニングと互換性」).
func TestDecodeRejectsUnknownNewerVersion(t *testing.T) {
	_, err := Decode([]byte(versionedEnvelopeJSON(SchemaVersion+1, true)))
	if err == nil {
		t.Fatal("expected a newer envelope version to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported envelope version") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestDecodeRejectsMissingData pins ADR-0015's alignment with
// spec/schemas/envelope.schema.json, which lists data as required. Decode
// used to default it silently to an empty object.
func TestDecodeRejectsMissingData(t *testing.T) {
	_, err := Decode([]byte(versionedEnvelopeJSON(SchemaVersion, false)))
	if err == nil {
		t.Fatal("expected an envelope without data to be rejected")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "data" {
		t.Errorf("expected a data validation error, got %v", err)
	}
}
