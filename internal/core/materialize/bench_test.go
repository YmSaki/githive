package materialize

import (
	"fmt"
	"testing"
	"time"

	"github.com/ymsaki/githive/internal/core/event"
)

const benchCrockford = "0123456789abcdefghjkmnpqrstvwxyz"

func benchULID(seq int64) string {
	var buf [26]byte
	for i := 25; i >= 0; i-- {
		buf[i] = benchCrockford[seq%32]
		seq /= 32
	}
	return string(buf[:])
}

func BenchmarkFoldIssue10kEvents(b *testing.B) {
	const total = 10000
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entity := benchULID(1)
	events := make([]*event.Envelope, total)
	events[0] = &event.Envelope{
		V:      event.SchemaVersion,
		Kind:   "issue.create",
		ID:     entity,
		TS:     base.Format("2006-01-02T15:04:05.000Z"),
		Actor:  "bench@example.com",
		Entity: entity,
		Data: map[string]any{
			"title": "10k event benchmark",
			"body":  "body",
		},
	}
	for i := 1; i < total; i++ {
		events[i] = &event.Envelope{
			V:      event.SchemaVersion,
			Kind:   "issue.comment",
			ID:     benchULID(int64(i + 1)),
			TS:     base.Add(time.Duration(i) * time.Millisecond).Format("2006-01-02T15:04:05.000Z"),
			Actor:  "bench@example.com",
			Entity: entity,
			Data: map[string]any{
				"body": fmt.Sprintf("comment %d", i),
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st := IssueRegistry.Fold(events)
		if st.Meta == nil {
			b.Fatal("nil meta")
		}
	}
}
