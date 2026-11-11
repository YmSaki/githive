package idgen

import (
	"bytes"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func TestGeneratorNewWithTimestampClockRollback(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 678000000, time.UTC)
	clock := base
	g := NewGenerator(func() time.Time { return clock }, bytes.NewReader(make([]byte, 20)))
	var previous string
	for i, delta := range []time.Duration{0, 0, -time.Second, -2 * time.Second, 0, time.Millisecond} {
		clock = base.Add(delta)
		id, ts := g.NewWithTimestamp()
		if len(id) != 26 || id != strings.ToLower(id) {
			t.Fatalf("ID is not a lowercase ULID: %q", id)
		}
		if previous != "" && id <= previous {
			t.Fatalf("call %d: ID %s did not increase from %s", i, id, previous)
		}
		u, err := ulid.ParseStrict(id)
		if err != nil {
			t.Fatal(err)
		}
		wantTime := base
		if delta > 0 {
			wantTime = clock
		}
		if want := ulid.Timestamp(wantTime); u.Time() != want {
			t.Fatalf("call %d: encoded time = %d, want %d", i, u.Time(), want)
		}
		if want := wantTime.Format("2006-01-02T15:04:05.000Z"); ts != want {
			t.Fatalf("call %d: timestamp = %s, want %s", i, ts, want)
		}
		previous = id
	}
}

func TestGeneratorNewWithTimestampConcurrent(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	g := NewGenerator(func() time.Time { return clock }, bytes.NewReader(make([]byte, 10)))
	const count = 256
	ids := make([]string, count)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], _ = g.NewWithTimestamp()
		}(i)
	}
	wg.Wait()
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("concurrent calls produced duplicate ID %s", ids[i])
		}
	}
}

func TestGeneratorNewWithTimestampCounterOverflow(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	g := NewGenerator(func() time.Time { return clock }, bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)))
	g.NewWithTimestamp()
	defer func() {
		if got := recover(); got != ulid.ErrMonotonicOverflow {
			t.Fatalf("overflow panic = %v, want %v", got, ulid.ErrMonotonicOverflow)
		}
	}()
	g.NewWithTimestamp()
}
