package event

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/ymsaki/githive/spec/schemas"
)

// DataSchemaError identifies data rejected by the authoritative kind schema.
type DataSchemaError struct {
	Kind  string
	Cause error
}

func (e *DataSchemaError) Error() string {
	return fmt.Sprintf("event: invalid data for %s: %v", e.Kind, e.Cause)
}
func (e *DataSchemaError) Unwrap() error { return e.Cause }

type dataContract struct {
	root, selected *jsonschema.Schema
	resolved       *jsonschema.Resolved
}

var contractsOnce sync.Once
var dataContracts map[string]dataContract
var contractsErr error

func loadDataContracts() {
	dataContracts = map[string]dataContract{}
	for _, feature := range []string{"issue", "task", "chat", "notify", "users", "meta"} {
		raw, err := schemas.Files.ReadFile(feature + ".schema.json")
		if err != nil {
			contractsErr = err
			return
		}
		var root jsonschema.Schema
		if err := json.Unmarshal(raw, &root); err != nil {
			contractsErr = err
			return
		}
		for kind, selected := range root.Defs {
			if !strings.HasPrefix(kind, feature+".") {
				continue
			}
			wrapper := &jsonschema.Schema{Schema: root.Schema, ID: root.ID, Defs: root.Defs, Ref: "#/$defs/" + kind}
			resolved, err := wrapper.Resolve(nil) // no loader: network/external refs are forbidden
			if err != nil {
				contractsErr = fmt.Errorf("compile %s: %w", kind, err)
				return
			}
			dataContracts[kind] = dataContract{root: &root, selected: selected, resolved: resolved}
		}
	}
}

// ValidateDataSchema reports whether kind is known and validates its data.
// Unknown kinds remain forward-compatible and are reported by callers as warnings.
// The instance is copied for numeric adaptation; no defaults or input mutation occur.
func ValidateDataSchema(env *Envelope) (known bool, err error) {
	if env == nil || env.Data == nil {
		return true, &DataSchemaError{Cause: fmt.Errorf("data must be a JSON object")}
	}
	contractsOnce.Do(loadDataContracts)
	if contractsErr != nil {
		return true, &DataSchemaError{env.Kind, contractsErr}
	}
	contract, ok := dataContracts[env.Kind]
	if !ok {
		return false, nil
	}
	if err := validateJSONValue(env.Data, 0); err != nil {
		return true, &DataSchemaError{env.Kind, err}
	}
	if err := checkExactIntegers(env.Data, contract.selected, contract.root.Defs); err != nil {
		return true, &DataSchemaError{env.Kind, err}
	}
	instance, err := schemaInstance(env.Data)
	if err == nil {
		err = contract.resolved.Validate(instance)
	}
	if err != nil {
		return true, &DataSchemaError{env.Kind, err}
	}
	return true, nil
}

// google/jsonschema-go treats json.Number as a string. Preserve exact integer
// classification before adapting to its numeric Go types, avoiding rounded
// fractions passing integer constraints. The rest of validation uses the schema.
func checkExactIntegers(value any, schema *jsonschema.Schema, defs map[string]*jsonschema.Schema) error {
	if schema == nil {
		return nil
	}
	if strings.HasPrefix(schema.Ref, "#/$defs/") {
		if err := checkExactIntegers(value, defs[strings.TrimPrefix(schema.Ref, "#/$defs/")], defs); err != nil {
			return err
		}
	}
	if schema.Type == "integer" {
		if n, ok := value.(json.Number); ok {
			if !exactSchemaInteger(n) {
				return fmt.Errorf("%s is not an integer", n)
			}
		}
	}
	if values, ok := value.(map[string]any); ok {
		for key, child := range schema.Properties {
			if v, present := values[key]; present {
				if err := checkExactIntegers(v, child, defs); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			}
		}
	}
	if values, ok := value.([]any); ok {
		for _, v := range values {
			if err := checkExactIntegers(v, schema.Items, defs); err != nil {
				return err
			}
		}
	}
	for _, child := range schema.AllOf {
		if err := checkExactIntegers(value, child, defs); err != nil {
			return err
		}
	}
	return nil
}

func schemaInstance(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("number out of range %q", v)
		}
		return f, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			n, err := schemaInstance(item)
			if err != nil {
				return nil, err
			}
			out[key] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			n, err := schemaInstance(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return value, nil
	}
}

// Classify decimal integers lexically, without rounding or constructing huge
// powers for adversarial exponents such as 1e-999999999.
func exactSchemaInteger(n json.Number) bool {
	s := strings.TrimPrefix(string(n), "-")
	mantissa, exponent := s, int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa = s[:i]
		var err error
		exponent, err = strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil {
			return strings.Trim(mantissa, "0.") == ""
		}
	}
	decimals := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		decimals = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	if strings.Trim(mantissa, "0") == "" {
		return true
	}
	if exponent >= int64(decimals) {
		return true
	}
	if exponent < -int64(len(mantissa)) {
		return false
	}
	scale := int64(decimals) - exponent
	return scale <= int64(len(mantissa)) && strings.Trim(mantissa[len(mantissa)-int(scale):], "0") == ""
}
