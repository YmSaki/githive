// Package event defines the githive event envelope and its canonical JSON
// encoding. This is the single place in the codebase allowed to implement
// canonical JSON; all other packages must go through Encode/Decode here
// instead of calling encoding/json directly to produce on-disk bytes
// (docs/02-data-model.md "canonical JSON", docs/13-roadmap.md 手戻りリスク表).
package event

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// CanonicalEncodeError is returned when a value cannot be represented in
// canonical JSON (e.g. NaN/Infinity, non-string map key, unsupported type).
type CanonicalEncodeError struct {
	msg string
}

func (e *CanonicalEncodeError) Error() string { return e.msg }

func encodeErrorf(format string, args ...any) error {
	return &CanonicalEncodeError{msg: fmt.Sprintf(format, args...)}
}

// Encode renders v as canonical JSON bytes, matching
// spec/reference/canonical_json.py byte-for-byte:
//   - object keys sorted by Unicode code point order
//   - 2-space indent, one trailing newline
//   - minimal string escaping, non-ASCII left as UTF-8
//   - numbers in shortest form
func Encode(v any) ([]byte, error) {
	s, err := EncodeString(v)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// EncodeString is the string form of Encode.
func EncodeString(v any) (string, error) {
	var b strings.Builder
	if err := encodeValue(&b, v, 0); err != nil {
		return "", err
	}
	b.WriteByte('\n')
	return b.String(), nil
}

func encodeValue(b *strings.Builder, v any, indent int) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
		return nil
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case string:
		encodeStringValue(b, x)
		return nil
	case json.Number:
		s, err := encodeJSONNumber(x)
		if err != nil {
			return err
		}
		b.WriteString(s)
		return nil
	case int:
		b.WriteString(strconv.Itoa(x))
		return nil
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
		return nil
	case float64:
		s, err := encodeFloat(x)
		if err != nil {
			return err
		}
		b.WriteString(s)
		return nil
	case map[string]any:
		return encodeObject(b, x, indent)
	case []any:
		return encodeArray(b, x, indent)
	case []string:
		arr := make([]any, len(x))
		for i, s := range x {
			arr[i] = s
		}
		return encodeArray(b, arr, indent)
	default:
		return encodeErrorf("unsupported type: %T", v)
	}
}

func encodeJSONNumber(n json.Number) (string, error) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		if s == "-0" {
			return "0", nil
		}
		return s, nil
	}
	f, err := n.Float64()
	if err != nil {
		return "", encodeErrorf("invalid number %q: %v", s, err)
	}
	return encodeFloat(f)
}

func encodeFloat(x float64) (string, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "", encodeErrorf("NaN/Infinity は JSON で表現できない: %v", x)
	}
	if x == math.Trunc(x) && math.Abs(x) < 1e16 {
		return strconv.FormatInt(int64(x), 10), nil
	}
	return strconv.FormatFloat(x, 'g', -1, 64), nil
}

func encodeStringValue(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func encodeObject(b *strings.Builder, obj map[string]any, indent int) error {
	if len(obj) == 0 {
		b.WriteString("{}")
		return nil
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	innerIndent := indent + 2
	pad := strings.Repeat(" ", innerIndent)
	closePad := strings.Repeat(" ", indent)

	b.WriteString("{\n")
	for i, k := range keys {
		b.WriteString(pad)
		encodeStringValue(b, k)
		b.WriteString(": ")
		if err := encodeValue(b, obj[k], innerIndent); err != nil {
			return err
		}
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(closePad)
	b.WriteByte('}')
	return nil
}

func encodeArray(b *strings.Builder, arr []any, indent int) error {
	if len(arr) == 0 {
		b.WriteString("[]")
		return nil
	}
	innerIndent := indent + 2
	pad := strings.Repeat(" ", innerIndent)
	closePad := strings.Repeat(" ", indent)

	b.WriteString("[\n")
	for i, v := range arr {
		b.WriteString(pad)
		if err := encodeValue(b, v, innerIndent); err != nil {
			return err
		}
		if i < len(arr)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(closePad)
	b.WriteByte(']')
	return nil
}

// DecodeGeneric parses raw JSON into canonical Go values (map[string]any,
// []any, json.Number, string, bool, nil), preserving number literals so that
// re-encoding via Encode round-trips through the same canonical form.
func DecodeGeneric(raw []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
