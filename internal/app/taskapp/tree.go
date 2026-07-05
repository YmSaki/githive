// Package taskapp implements the task feature's application service:
// read/write operations over refs/projects/task/<id> (docs/features/task.md).
package taskapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// TreeFiles renders a fold State into the task tree layout
// (docs/features/task.md「ref とツリー」): meta.json (state without the
// body field), body.md, and notes/<event-id>.md files with front matter.
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	if state.Meta == nil {
		return nil, fmt.Errorf("taskapp: cannot render tree for a non-existent task")
	}
	files := map[string][]byte{}

	metaCopy := make(map[string]any, len(state.Meta))
	body := ""
	for k, v := range state.Meta {
		if k == "body" {
			if s, ok := v.(string); ok {
				body = s
			}
			continue
		}
		metaCopy[k] = v
	}
	metaJSON, err := event.Encode(metaCopy)
	if err != nil {
		return nil, fmt.Errorf("taskapp: encode meta.json: %w", err)
	}
	files["meta.json"] = metaJSON
	files["body.md"] = []byte(body)

	for id, raw := range state.Collections["notes"] {
		note, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		files["notes/"+id+".md"] = renderNote(note)
	}
	return files, nil
}

func renderNote(note map[string]any) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	if author, ok := note["author"].(string); ok && author != "" {
		fmt.Fprintf(&b, "author: %s\n", author)
	}
	if ts, ok := note["ts"].(string); ok && ts != "" {
		fmt.Fprintf(&b, "ts: %s\n", ts)
	}
	if id, ok := note["id"].(string); ok && id != "" {
		fmt.Fprintf(&b, "event: %s\n", id)
	}
	b.WriteString("---\n\n")
	if body, ok := note["body"].(string); ok {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// sortedNoteIDs is a small helper for deterministic iteration over a notes
// collection (map iteration order in Go is randomized).
func sortedNoteIDs(notes map[string]any) []string {
	ids := make([]string, 0, len(notes))
	for id := range notes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
