// Package notifyapp implements the notify feature's application service:
// read/write operations over refs/projects/notify/stream
// (docs/features/notify.md).
package notifyapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// TreeFiles renders a fold State into the notify tree layout
// (docs/features/notify.md「ref とツリー」): meta.json, one
// events/<yyyy-mm>.jsonl per month with one compact JSON line per post
// (sorted by event ID so ULID order gives chronological order), and
// acks.json ({"<notify-event-id>": ["actor", ...]}).
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	meta := state.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := event.Encode(map[string]any(meta))
	if err != nil {
		return nil, fmt.Errorf("notifyapp: encode meta.json: %w", err)
	}
	files := map[string][]byte{"meta.json": metaJSON}

	byMonth := map[string][]string{} // month -> sorted post IDs
	for id, raw := range state.Collections["posts"] {
		post, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		month, _ := post["month"].(string)
		if month == "" {
			month = "unknown"
		}
		byMonth[month] = append(byMonth[month], id)
	}
	for month, ids := range byMonth {
		sort.Strings(ids)
		var b strings.Builder
		for _, id := range ids {
			line, err := event.EncodeCompactLine(state.Collections["posts"][id])
			if err != nil {
				return nil, fmt.Errorf("notifyapp: encode post %s: %w", id, err)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		files["events/"+month+".jsonl"] = []byte(b.String())
	}

	acks := materialize.NotifyAcks(state)
	acksAny := make(map[string]any, len(acks))
	for k, v := range acks {
		acksAny[k] = v
	}
	acksJSON, err := event.Encode(acksAny)
	if err != nil {
		return nil, fmt.Errorf("notifyapp: encode acks.json: %w", err)
	}
	files["acks.json"] = acksJSON

	return files, nil
}
