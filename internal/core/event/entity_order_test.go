package event

import (
	"errors"
	"testing"
)

func TestValidateEntityOrderRejectsSilentLossAndTakeover(t *testing.T) {
	for _, feature := range []string{"issue", "task", "chat"} {
		t.Run(feature, func(t *testing.T) {
			create := &Envelope{ID: "002", Entity: "entity", Kind: feature + ".create"}
			old := &Envelope{ID: "001", Entity: "entity", Kind: feature + ".comment"}
			later := &Envelope{ID: "003", Entity: "entity", Kind: feature + ".comment"}
			checkpoint := &Envelope{ID: "000", Entity: "entity", Kind: feature + ".checkpoint"}
			if err := ValidateEntityOrder([]*Envelope{later, checkpoint, create, create}); err != nil {
				t.Fatalf("ordinary unordered history and identical create copies must pass: %v", err)
			}
			if err := ValidateEntityOrder([]*Envelope{create, old}); !errors.Is(err, ErrEventOrderViolation) {
				t.Fatalf("pre-create mutation must be explicit error: %v", err)
			}
			for _, id := range []string{"001", "004"} {
				duplicateCreate := &Envelope{ID: id, Entity: "entity", Kind: feature + ".create"}
				if err := ValidateEntityOrder([]*Envelope{create, later, duplicateCreate}); !errors.Is(err, ErrEventOrderViolation) {
					t.Fatalf("distinct create %s must not replace or hide behind original: %v", id, err)
				}
			}
		})
	}
}
