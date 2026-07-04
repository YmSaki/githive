// Package chatapp implements the chat feature's application service:
// read/write operations over refs/projects/chat/<id> (docs/features/chat.md).
package chatapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// TreeFiles renders a fold State into the chat tree layout
// (docs/features/chat.md「ref とツリー」): meta.json and
// messages/<event-id>.md files with front matter. Unlike issue/task, chat's
// meta has no separate body field - a create's body becomes a message
// instead (docs/features/chat.md「イベント定義」).
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	if state.Meta == nil {
		return nil, fmt.Errorf("chatapp: cannot render tree for a non-existent chat thread")
	}
	files := map[string][]byte{}

	metaJSON, err := event.Encode(map[string]any(state.Meta))
	if err != nil {
		return nil, fmt.Errorf("chatapp: encode meta.json: %w", err)
	}
	files["meta.json"] = metaJSON

	for id, raw := range state.Collections["messages"] {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		files["messages/"+id+".md"] = renderMessage(message)
	}
	return files, nil
}

func renderMessage(message map[string]any) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	writeFrontMatterField(&b, message, "author")
	writeFrontMatterField(&b, message, "ts")
	writeFrontMatterField(&b, message, "id", "event")
	writeFrontMatterField(&b, message, "reply_to")
	writeFrontMatterField(&b, message, "supersedes")
	writeFrontMatterField(&b, message, "superseded_by")
	b.WriteString("---\n\n")
	if body, ok := message["body"].(string); ok {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

func writeFrontMatterField(b *strings.Builder, message map[string]any, key string, label ...string) {
	v, ok := message[key].(string)
	if !ok || v == "" {
		return
	}
	name := key
	if len(label) > 0 {
		name = label[0]
	}
	fmt.Fprintf(b, "%s: %s\n", name, v)
}

// sortedMessageIDs is a small helper for deterministic iteration over a
// messages collection (map iteration order in Go is randomized).
func sortedMessageIDs(messages map[string]any) []string {
	ids := make([]string, 0, len(messages))
	for id := range messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
