// Package idgen generates event/entity IDs: lowercase ULIDs, monotonic
// within this process so that events created back-to-back (even within the
// same millisecond or after a clock rollback) still sort in creation order
// (docs/02-data-model.md「ID：ULID」,
// docs/03-sync-and-concurrency.md「時計異常への防御」).
package idgen

import (
	"crypto/rand"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Generator serializes ID creation and keeps its clock from moving backwards.
// A Generator must not be copied after first use.
type Generator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy io.Reader
	last    ulid.ULID
	hasLast bool
}

// NewGenerator returns an independent generator using the supplied clock and
// entropy source. Nil arguments use time.Now and crypto/rand.Reader.
func NewGenerator(now func() time.Time, entropy io.Reader) *Generator {
	if now == nil {
		now = time.Now
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	return &Generator{now: now, entropy: entropy}
}

var defaultGenerator = NewGenerator(nil, nil)

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
	return defaultGenerator.NewWithTimestamp()
}

// NewWithTimestamp returns a fresh lowercase ULID and its encoded timestamp.
// IDs increase strictly across calls, including when the clock moves backwards.
// Entropy failures and exhaustion of the 80-bit counter panic, as with the
// package-level functions.
func (g *Generator) NewWithTimestamp() (id string, ts string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := ulid.Timestamp(g.now())
	var u ulid.ULID
	if g.hasLast && now <= g.last.Time() {
		// Increment the full random field ourselves: ulid.Monotonic uses an
		// all-zero field as an initialization sentinel and can repeat IDs
		// when a valid entropy source returns zero bytes.
		u = g.last
		for i := len(u) - 1; i >= 6; i-- {
			u[i]++
			if u[i] != 0 {
				break
			}
			if i == 6 {
				panic(ulid.ErrMonotonicOverflow)
			}
		}
	} else {
		u = ulid.MustNew(now, g.entropy)
	}
	g.last = u
	g.hasLast = true
	return strings.ToLower(u.String()), ulid.Time(u.Time()).UTC().Format("2006-01-02T15:04:05.000Z")
}
