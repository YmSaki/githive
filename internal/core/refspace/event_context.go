package refspace

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
)

// ErrEventRefMismatch means an event is stored under another feature/entity.
var ErrEventRefMismatch = errors.New("refspace: event does not belong to ref")

// ValidateEventContext checks namespace constraints without changing reducers.
func ValidateEventContext(ref ParsedRef, env *event.Envelope) error {
	if env == nil || ref.Feature == FeatureWiki {
		return nil
	}
	if !strings.HasPrefix(env.Kind, string(ref.Feature)+".") {
		return fmt.Errorf("%w: kind %q differs from ref feature %q", ErrEventRefMismatch, env.Kind, ref.Feature)
	}
	if ref.ID != "" && env.Entity != ref.ID {
		return fmt.Errorf("%w: event entity %q differs from ref entity %q", ErrEventRefMismatch, env.Entity, ref.ID)
	}
	return nil
}
