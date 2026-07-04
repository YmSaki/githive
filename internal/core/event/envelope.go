package event

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// SchemaVersion is the only envelope schema version githive currently emits
// or accepts. docs/02-data-model.md: 未知の v は読み取り拒否する。
const SchemaVersion = 1

var (
	kindRe  = regexp.MustCompile(`^[a-z]+\.[a-z_]+$`)
	ulidRe  = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{26}$`)
	tsRe    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$`)
	actorRe = regexp.MustCompile(`^[^\s@]+@[^\s]+$`)
)

// IsValidULID reports whether s is a 26-char lowercase Crockford Base32
// ULID, per docs/02-data-model.md "ID：ULID" and
// spec/schemas/envelope.schema.json.
func IsValidULID(s string) bool {
	return ulidRe.MatchString(s)
}

// Envelope is the common event envelope (docs/02-data-model.md「イベント封筒」).
type Envelope struct {
	V      int
	Kind   string
	ID     string
	TS     string
	Actor  string
	Entity string
	Data   map[string]any

	// Extra holds unknown top-level fields so that readers preserve them
	// on write-back (docs/02-data-model.md: 読み手は未知フィールドを無視し、
	// 書き換え時も保存する).
	Extra map[string]any
}

// ValidationError reports a single envelope field failing validation.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("event envelope: field %q: %s", e.Field, e.Msg)
}

// Validate checks the envelope against docs/02-data-model.md /
// spec/schemas/envelope.schema.json.
func (e *Envelope) Validate() error {
	if e.V != SchemaVersion {
		return &ValidationError{"v", fmt.Sprintf("unsupported envelope version %d (want %d)", e.V, SchemaVersion)}
	}
	if !kindRe.MatchString(e.Kind) {
		return &ValidationError{"kind", fmt.Sprintf("must match ^[a-z]+\\.[a-z_]+$, got %q", e.Kind)}
	}
	if !ulidRe.MatchString(e.ID) {
		return &ValidationError{"id", fmt.Sprintf("must be a 26-char lowercase ULID, got %q", e.ID)}
	}
	if !tsRe.MatchString(e.TS) {
		return &ValidationError{"ts", fmt.Sprintf("must be RFC3339 UTC millisecond precision, got %q", e.TS)}
	}
	if len(e.Actor) < 3 || len(e.Actor) > 320 {
		return &ValidationError{"actor", fmt.Sprintf("must be 3-320 bytes, got %d", len(e.Actor))}
	}
	if !actorRe.MatchString(e.Actor) {
		return &ValidationError{"actor", fmt.Sprintf("must contain no whitespace and an '@', got %q", e.Actor)}
	}
	if !ulidRe.MatchString(e.Entity) {
		return &ValidationError{"entity", fmt.Sprintf("must be a 26-char lowercase ULID, got %q", e.Entity)}
	}
	if e.Data == nil {
		return &ValidationError{"data", "must be present (may be empty object)"}
	}
	return nil
}

// ToMap renders the envelope (including preserved unknown fields) as a
// generic value suitable for Encode.
func (e *Envelope) ToMap() map[string]any {
	m := make(map[string]any, len(e.Extra)+7)
	for k, v := range e.Extra {
		m[k] = v
	}
	m["v"] = e.V
	m["kind"] = e.Kind
	m["id"] = e.ID
	m["ts"] = e.TS
	m["actor"] = e.Actor
	m["entity"] = e.Entity
	if e.Data == nil {
		m["data"] = map[string]any{}
	} else {
		m["data"] = e.Data
	}
	return m
}

// CanonicalJSON encodes the envelope as canonical JSON bytes.
func (e *Envelope) CanonicalJSON() ([]byte, error) {
	return Encode(e.ToMap())
}

// Decode parses raw JSON bytes (as found in a commit message body) into an
// Envelope, preserving unknown fields, and validates it.
func Decode(raw []byte) (*Envelope, error) {
	v, err := DecodeGeneric(raw)
	if err != nil {
		return nil, fmt.Errorf("event: decode: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("event: decode: top-level value is not an object")
	}
	return FromMap(m)
}

// FromMap builds an Envelope from a generic decoded JSON object, validating
// the result.
func FromMap(m map[string]any) (*Envelope, error) {
	e := &Envelope{Extra: map[string]any{}}
	for k, v := range m {
		switch k {
		case "v":
			n, err := asInt(v)
			if err != nil {
				return nil, &ValidationError{"v", err.Error()}
			}
			e.V = n
		case "kind":
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{"kind", "must be a string"}
			}
			e.Kind = s
		case "id":
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{"id", "must be a string"}
			}
			e.ID = s
		case "ts":
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{"ts", "must be a string"}
			}
			e.TS = s
		case "actor":
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{"actor", "must be a string"}
			}
			e.Actor = s
		case "entity":
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{"entity", "must be a string"}
			}
			e.Entity = s
		case "data":
			d, ok := v.(map[string]any)
			if !ok {
				return nil, &ValidationError{"data", "must be an object"}
			}
			e.Data = d
		default:
			e.Extra[k] = v
		}
	}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("not an integer: %v", n)
		}
		return int(i), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
