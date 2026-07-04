package issueapp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// minIDPrefixLen is the shortest ID prefix githive resolves by itself
// (docs/10-cli-spec.md「ID の入力解決」: 先頭 8 文字以上の前方一致).
const minIDPrefixLen = 8

// ResolveID resolves a full or shortened (>=8 char) ULID to the full ID of
// exactly one existing issue. Returns *AmbiguousIDError if more than one
// issue matches, or ErrNotFound if none do.
func (s *Service) ResolveID(ctx context.Context, prefix string) (string, error) {
	if event.IsValidULID(prefix) {
		r := gitx.New(s.Dir)
		oid, err := r.RevParse(ctx, refFor(prefix).String())
		if err != nil {
			return "", err
		}
		if oid == "" {
			return "", ErrNotFound
		}
		return prefix, nil
	}
	if len(prefix) < minIDPrefixLen {
		return "", fmt.Errorf("issueapp: id prefix %q is shorter than %d characters", prefix, minIDPrefixLen)
	}

	ids, err := s.listIDs(ctx)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", ErrNotFound
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", &AmbiguousIDError{Prefix: prefix, Candidates: matches}
	}
}

func (s *Service) listIDs(ctx context.Context) ([]string, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/issue/")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureIssue {
			continue
		}
		ids = append(ids, parsed.ID)
	}
	return ids, nil
}

// ListFilter narrows List results. Zero value means "no filter".
type ListFilter struct {
	Status   string
	Label    string
	Assignee string
}

// List returns a snapshot summary of every issue, using the fast
// snapshot-read path (meta.json at the ref head) rather than a full
// history fold (docs/01-architecture.md「読み取り（高速路と互換路）」).
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Meta, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/issue/")
	if err != nil {
		return nil, err
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, err
	}

	var out []Meta
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureIssue {
			continue
		}
		files, err := chain.ReadTree(repo, plumbing.NewHash(e.OID))
		if err != nil {
			return nil, err
		}
		metaRaw, ok := files["meta.json"]
		if !ok {
			continue
		}
		decoded, err := event.DecodeGeneric(metaRaw)
		if err != nil {
			return nil, fmt.Errorf("issueapp: decode %s meta.json: %w", parsed.ID, err)
		}
		meta, ok := decoded.(map[string]any)
		if !ok {
			continue
		}
		if !matchesFilter(meta, filter) {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["id"]) < fmt.Sprint(out[j]["id"])
	})
	return out, nil
}

func matchesFilter(meta Meta, filter ListFilter) bool {
	if filter.Status != "" {
		if s, _ := meta["status"].(string); s != filter.Status {
			return false
		}
	}
	if filter.Label != "" {
		if !containsString(meta["labels"], filter.Label) {
			return false
		}
	}
	if filter.Assignee != "" {
		if !containsString(meta["assignees"], filter.Assignee) {
			return false
		}
	}
	return true
}

func containsString(v any, want string) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range arr {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}

// Show returns the full state of one issue (meta, body, comments),
// reconstructed by folding the issue's complete event history
// (docs/01-architecture.md「イベント読み」).
func (s *Service) Show(ctx context.Context, id string) (*Show, error) {
	events, _, err := s.currentEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	state := materialize.IssueRegistry.Fold(events)
	if state.Meta == nil {
		return nil, ErrNotFound
	}

	metaCopy := make(map[string]any, len(state.Meta))
	body := ""
	for k, v := range state.Meta {
		if k == "body" {
			if b, ok := v.(string); ok {
				body = b
			}
			continue
		}
		metaCopy[k] = v
	}

	var comments []Comment
	for _, cid := range sortedCommentIDs(state.Collections["comments"]) {
		if c, ok := state.Collections["comments"][cid].(map[string]any); ok {
			comments = append(comments, c)
		}
	}

	return &Show{Meta: metaCopy, Body: body, Comments: comments}, nil
}
