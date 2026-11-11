package event

import (
	"errors"
	"os"
	"testing"
)

func TestUsersKeyOrderDomainVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/vectors/domain-validation/01-users-key-order.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := DecodeGeneric(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range value.(map[string]any)["cases"].([]any) {
		vector := input.(map[string]any)
		t.Run(vector["name"].(string), func(t *testing.T) {
			var events []*Envelope
			for _, data := range vector["events"].([]any) {
				env, err := FromMap(data.(map[string]any))
				if err != nil {
					t.Fatal(err)
				}
				events = append(events, env)
			}
			for _, reverse := range []bool{false, true} {
				inputs := append([]*Envelope(nil), events...)
				if reverse {
					for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
						inputs[i], inputs[j] = inputs[j], inputs[i]
					}
				}
				var ids IDSet
				for _, env := range inputs {
					if _, err := ids.Add(env); err != nil {
						t.Fatal(err)
					}
				}
				err := ValidateEntityOrder(inputs)
				if valid := vector["valid"].(bool); (err == nil) != valid || err != nil && !errors.Is(err, ErrEventOrderViolation) {
					t.Fatalf("reverse=%t valid=%t error=%v", reverse, valid, err)
				}
			}
		})
	}
}
