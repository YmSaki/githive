package event

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrConflictingEventID means one event ID names different canonical payloads.
// Such an event set is corrupt; input order must never decide a winner.
var ErrConflictingEventID = errors.New("event: conflicting_event_id")

type idEntry struct {
	env       *Envelope
	canonical []byte
}

// IDSet validates event identity at history and merge boundaries. The zero
// value is ready to use. Identical copies are accepted and reported as seen;
// all envelope fields, including unknown extension fields, participate.
//
// CanonicalJSON is computed lazily only when an ID is seen a second time, so
// the first Add does not validate encodability (callers passing freshly built
// envelopes rather than event.Decode outputs must validate CanonicalJSON first).
type IDSet struct{ entries map[string]*idEntry }

// Add returns true for the first occurrence of env.ID and false for an
// identical copy, returning ErrConflictingEventID if a second occurrence differs.
//
// For performance, the first occurrence stores env without computing
// CanonicalJSON; callers must treat envelopes as immutable after Add and must
// validate freshly built envelopes (e.g. via CanonicalJSON) before folding.
func (s *IDSet) Add(env *Envelope) (bool, error) {
	if env == nil {
		return false, fmt.Errorf("event: nil envelope")
	}
	if prior, ok := s.entries[env.ID]; ok {
		if prior.canonical == nil {
			raw, err := prior.env.CanonicalJSON()
			if err != nil {
				return false, err
			}
			prior.canonical = raw
		}
		raw, err := env.CanonicalJSON()
		if err != nil {
			return false, err
		}
		if !bytes.Equal(prior.canonical, raw) {
			return false, fmt.Errorf("%w: %s", ErrConflictingEventID, env.ID)
		}
		return false, nil
	}
	if s.entries == nil {
		s.entries = map[string]*idEntry{}
	}
	s.entries[env.ID] = &idEntry{env: env}
	return true, nil
}
