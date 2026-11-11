package merge

import (
	"errors"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func TestUnionRejectsUnsafeEntityOrderAcrossSeparateHistories(t *testing.T) {
	create := issueCreateEvent("entity", 10)
	for _, conflicting := range []*event.Envelope{issueCreateEvent("entity", 20), issueLabelEvent("entity", 5, "before-create")} {
		if _, err := UnionEvents([]*event.Envelope{create}, []*event.Envelope{conflicting}); !errors.Is(err, event.ErrEventOrderViolation) {
			t.Fatalf("separately read histories must not combine into silent loss/takeover: %v", err)
		}
	}
}
