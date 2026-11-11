package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// MaxJSONDepth bounds container nesting in both canonical encodings and input
// JSON. It prevents pathological indentation growth and recursive processing.
const MaxJSONDepth = 128

// validateJSONValue runs before encoding, including for values constructed by
// callers rather than decoded from JSON. Cyclic containers hit the depth bound.
func validateJSONValue(v any, depth int) error {
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) {
			return encodeErrorf("invalid UTF-8 string")
		}
	case map[string]any:
		if depth >= MaxJSONDepth {
			return encodeErrorf("JSON nesting exceeds %d containers", MaxJSONDepth)
		}
		for k, v := range x {
			if !utf8.ValidString(k) {
				return encodeErrorf("invalid UTF-8 object key")
			}
			if err := validateJSONValue(v, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if depth >= MaxJSONDepth {
			return encodeErrorf("JSON nesting exceeds %d containers", MaxJSONDepth)
		}
		for _, v := range x {
			if err := validateJSONValue(v, depth+1); err != nil {
				return err
			}
		}
	case []string:
		if depth >= MaxJSONDepth {
			return encodeErrorf("JSON nesting exceeds %d containers", MaxJSONDepth)
		}
		for _, v := range x {
			if !utf8.ValidString(v) {
				return encodeErrorf("invalid UTF-8 string")
			}
		}
	}
	return nil
}

func decodeStrict(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("JSON contains invalid UTF-8")
	}
	if err := checkSurrogateEscapes(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJSONValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return v, nil
}

func decodeJSONValue(dec *json.Decoder, depth int) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	if depth >= MaxJSONDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d containers", MaxJSONDepth)
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := out[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q", key)
			}
			value, err := decodeJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		close, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if close != json.Delim('}') {
			return nil, fmt.Errorf("unclosed JSON object")
		}
		return out, nil
	case '[':
		out := []any{}
		for dec.More() {
			value, err := decodeJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		close, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if close != json.Delim(']') {
			return nil, fmt.Errorf("unclosed JSON array")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// encoding/json replaces lone escaped surrogates with U+FFFD. Reject them
// before tokenization so distinct invalid inputs cannot silently change value.
func checkSurrogateEscapes(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); {
		if raw[i] == '"' {
			inString = !inString
			i++
			continue
		}
		if !inString || raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return nil
		} // normal decoder reports truncated escapes
		if raw[i+1] != 'u' || i+6 > len(raw) {
			i += 2
			continue
		}
		n, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			i += 2
			continue
		}
		if n >= 0xdc00 && n <= 0xdfff {
			return fmt.Errorf("unpaired low Unicode surrogate")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return fmt.Errorf("unpaired high Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired high Unicode surrogate")
			}
			i += 12
			continue
		}
		i += 6
	}
	return nil
}
