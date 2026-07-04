package event

import "sort"

// SortEvents returns a new slice of events ordered by ID (ULID) ascending,
// which docs/02-data-model.md defines as the total order events fold in:
// state = fold(sort_by_event_id(events)). The sort is stable so duplicate
// IDs (which should not normally occur) keep their input order.
func SortEvents(events []*Envelope) []*Envelope {
	out := make([]*Envelope, len(events))
	copy(out, events)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
