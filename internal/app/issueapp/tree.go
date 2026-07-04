// Package issueapp implements the issue feature's application service:
// read/write operations over refs/projects/issue/<id>
// (docs/features/issue.md).
package issueapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// TreeFiles renders a fold State into the issue tree layout
// (docs/features/issue.md「ref とツリー」): meta.json (state without the
// body field), body.md, and comments/<event-id>.md files with a small YAML
// front matter block for human/grep readability
// (docs/02-data-model.md「実体化ツリーの共通配置」).
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	if state.Meta == nil {
		return nil, fmt.Errorf("issueapp: cannot render tree for a non-existent issue")
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
		return nil, fmt.Errorf("issueapp: encode meta.json: %w", err)
	}
	files["meta.json"] = metaJSON
	files["body.md"] = []byte(body)

	for id, raw := range state.Collections["comments"] {
		comment, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		files["comments/"+id+".md"] = renderComment(comment)
	}
	return files, nil
}

func renderComment(comment map[string]any) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	writeFrontMatterField(&b, comment, "author")
	writeFrontMatterField(&b, comment, "ts")
	writeFrontMatterField(&b, comment, "id", "event")
	writeFrontMatterField(&b, comment, "reply_to")
	writeFrontMatterField(&b, comment, "supersedes")
	writeFrontMatterField(&b, comment, "superseded_by")
	b.WriteString("---\n\n")
	if body, ok := comment["body"].(string); ok {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// writeFrontMatterField writes "<label>: <value>\n" if comment[key] is a
// non-empty string. label defaults to key unless an override is given
// (used to rename the comment's own "id" field to "event" in the front
// matter, per docs/features/issue.md「comments/<event-id>.md」example).
func writeFrontMatterField(b *strings.Builder, comment map[string]any, key string, label ...string) {
	v, ok := comment[key].(string)
	if !ok || v == "" {
		return
	}
	name := key
	if len(label) > 0 {
		name = label[0]
	}
	fmt.Fprintf(b, "%s: %s\n", name, v)
}

// sortedCommentIDs is a small helper kept for callers that want a
// deterministic iteration order over a comments collection (map iteration
// order in Go is randomized).
func sortedCommentIDs(comments map[string]any) []string {
	ids := make([]string, 0, len(comments))
	for id := range comments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
