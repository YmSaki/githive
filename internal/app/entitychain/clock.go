package entitychain

import (
	"fmt"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
)

// ClockSkewError means a new mutation sorts before known history and could
// silently lose to it. No event or ref update has been persisted.
type ClockSkewError struct {
	EventID         string
	LatestID        string
	LatestTimestamp string
}

func (e *ClockSkewError) Error() string {
	return fmt.Sprintf("entitychain: clock_skew: new event %s must sort after known event %s (%s); %s", e.EventID, e.LatestID, e.LatestTimestamp, e.Recovery())
}

// Recovery describes how the operator can retry without rewriting history.
func (e *ClockSkewError) Recovery() string {
	return "no event was recorded; check this machine's clock and the originating peer's clock, then retry the original write once its new ID sorts after the known history"
}

func validateNewEventOrder(env *event.Envelope, history []*event.Envelope) error {
	if strings.HasSuffix(env.Kind, ".checkpoint") {
		return nil // checkpoints are ignored by reducers and have no mutation order
	}
	var latest *event.Envelope
	for _, prior := range history {
		if strings.HasSuffix(prior.Kind, ".checkpoint") {
			continue
		}
		if latest == nil || prior.ID > latest.ID {
			latest = prior
		}
	}
	if latest != nil && env.ID <= latest.ID {
		return &ClockSkewError{EventID: env.ID, LatestID: latest.ID, LatestTimestamp: latest.TS}
	}
	return nil
}
