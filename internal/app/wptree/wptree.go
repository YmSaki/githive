// Package wptree renders and re-reads the workpackage tree layout
// (refs/projects/workpackage/<id>: meta.json, body.md, notes/<event-id>.md).
// It is separate from workpackageapp so that syncapp/fsckapp can render
// workpackage trees without importing the service (which itself uses
// syncapp for wp start's pre-fetch).
package wptree

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// noteKeys is the fixed front-matter key order.
var noteKeys = []string{"event_id", "kind", "actor", "ts"}

// TreeFiles renders a fold state into the workpackage tree.
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	if state.Meta == nil {
		return nil, fmt.Errorf("wptree: cannot render tree for a non-existent workpackage")
	}
	metaJSON, err := event.Encode(state.Meta)
	if err != nil {
		return nil, fmt.Errorf("wptree: encode meta.json: %w", err)
	}
	text, _ := state.Collections["body"]["text"].(string)
	files := map[string][]byte{
		"meta.json": metaJSON,
		"body.md":   []byte(text),
	}
	for id, raw := range state.Collections["notes"] {
		note, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rendered, err := RenderNote(note)
		if err != nil {
			return nil, err
		}
		files["notes/"+id+".md"] = rendered
	}
	return files, nil
}

// RenderNote renders one note: YAML front matter whose string values use the
// canonical JSON string encoding, a blank line, then the body with exactly
// one trailing LF added when it lacks one.
func RenderNote(note map[string]any) ([]byte, error) {
	var b strings.Builder
	b.WriteString("---\n")
	for _, key := range noteKeys {
		value, _ := note[key].(string)
		quoted, err := event.EncodeCompactLine(value)
		if err != nil {
			return nil, fmt.Errorf("wptree: encode note %s: %w", key, err)
		}
		b.WriteString(key + ": " + quoted + "\n")
	}
	b.WriteString("---\n\n")
	body, _ := note["body"].(string)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// ParseNote decodes a rendered note back into its front matter fields and
// rendered body (the persisted trailing LF is kept verbatim).
func ParseNote(raw []byte, id string) (map[string]any, error) {
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("wptree: missing front matter")
	}
	end := strings.Index(text[4:], "---\n\n")
	if end < 0 {
		return nil, fmt.Errorf("wptree: unterminated front matter")
	}
	end += 4
	note := map[string]any{"body": text[end+5:], "body_source": "rendered_markdown"}
	lines := strings.Split(strings.TrimSuffix(text[4:end], "\n"), "\n")
	if len(lines) != len(noteKeys) {
		return nil, fmt.Errorf("wptree: front matter must have %d fields", len(noteKeys))
	}
	for i, line := range lines {
		key, value, ok := strings.Cut(line, ": ")
		if !ok || key != noteKeys[i] {
			return nil, fmt.Errorf("wptree: invalid front matter line %q", line)
		}
		decoded, err := event.DecodeGeneric([]byte(value))
		if err != nil {
			return nil, fmt.Errorf("wptree: front matter %s: %w", key, err)
		}
		s, ok := decoded.(string)
		if !ok {
			return nil, fmt.Errorf("wptree: front matter %s is not a string", key)
		}
		note[key] = s
	}
	if note["event_id"] != id {
		return nil, fmt.Errorf("wptree: note ID mismatch")
	}
	return note, nil
}

// SortedNoteIDs returns note IDs in ascending order.
func SortedNoteIDs(notes map[string]any) []string {
	ids := make([]string, 0, len(notes))
	for id := range notes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
