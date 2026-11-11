package usersapp

import (
	"errors"
	"fmt"
)

// ErrInvalidPolicy means the policy does not match users.policy_set's data
// schema. It is checked by every caller through Service.PolicySet.
var ErrInvalidPolicy = errors.New("usersapp: invalid policy")

// ValidatePolicy checks the shape and enums in spec/schemas/users.schema.json.
// It does not evaluate authorization or impose stronger principal/ref syntax
// than that schema. Additional rule fields are retained and allowed.
func ValidatePolicy(rules []any, def string) error {
	if def != "allow" && def != "deny" {
		return fmt.Errorf("%w: default must be allow or deny", ErrInvalidPolicy)
	}
	if rules == nil {
		return fmt.Errorf("%w: rules must be an array, not null or missing", ErrInvalidPolicy)
	}
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok || rule == nil {
			return fmt.Errorf("%w: rules[%d] must be an object", ErrInvalidPolicy, i)
		}
		if _, ok := rule["refs"].(string); !ok {
			return fmt.Errorf("%w: rules[%d].refs must be a string", ErrInvalidPolicy, i)
		}
		for _, field := range []string{"allow", "actions"} {
			values, ok := policyStrings(rule[field])
			if !ok {
				return fmt.Errorf("%w: rules[%d].%s must be an array of strings", ErrInvalidPolicy, i, field)
			}
			if field == "actions" {
				for _, action := range values {
					switch action {
					case "push", "create", "delete", "force":
					default:
						return fmt.Errorf("%w: rules[%d].actions contains unknown action %q", ErrInvalidPolicy, i, action)
					}
				}
			}
		}
	}
	return nil
}

func policyStrings(raw any) ([]string, bool) {
	switch values := raw.(type) {
	case []string:
		return values, values != nil
	case []any:
		if values == nil {
			return nil, false
		}
		out := make([]string, len(values))
		for i, raw := range values {
			value, ok := raw.(string)
			if !ok {
				return nil, false
			}
			out[i] = value
		}
		return out, true
	default:
		return nil, false
	}
}
