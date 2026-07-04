// Package idgen generates event/entity IDs: lowercase ULIDs, monotonic
// within this process so that events created back-to-back (even within the
// same millisecond) still sort in creation order
// (docs/02-data-model.md「ID：ULID」,
// docs/03-sync-and-concurrency.md「時計異常への防御」).
package idgen

import (
	"crypto/rand"
	"strings"
	"sync"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a fresh lowercase ULID string.
func New() string {
	id, _ := NewWithTimestamp()
	return id
}

// NewWithTimestamp returns a fresh lowercase ULID plus the RFC3339
// UTC-millisecond timestamp string encoded in it, matching the envelope
// requirement that ts equal the ULID's embedded time
// (docs/02-data-model.md「イベント封筒」).
func NewWithTimestamp() (id string, ts string) {
	mu.Lock()
	defer mu.Unlock()
	now := ulid.Now()
	u := ulid.MustNew(now, entropy)
	id = strings.ToLower(u.String())
	ts = ulid.Time(now).UTC().Format("2006-01-02T15:04:05.000Z")
	return id, ts
}
