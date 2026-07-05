package taskapp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// minIDPrefixLen is the shortest ID prefix githive resolves by itself
// (docs/10-cli-spec.md「ID の入力解決」: 先頭 8 文字以上の前方一致).
const minIDPrefixLen = 8

// ResolveID resolves a full or shortened (>=8 char) ULID to the full ID of
// exactly one existing task.
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
		return "", fmt.Errorf("taskapp: id prefix %q is shorter than %d characters", prefix, minIDPrefixLen)
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
	entries, err := r.ForEachRef(ctx, "refs/projects/task/")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureTask {
			continue
		}
		ids = append(ids, parsed.ID)
	}
	return ids, nil
}

// ListFilter narrows List results. Zero value means "no filter". Mine, when
// set, matches meta.owner against actorEmail.
type ListFilter struct {
	Status     string
	Owner      string
	Mine       bool
	ActorEmail string
}

// List returns a snapshot summary of every task, using the fast
// snapshot-read path (meta.json at the ref head).
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Meta, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/task/")
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
		if err != nil || parsed.Feature != refspace.FeatureTask {
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
			return nil, fmt.Errorf("taskapp: decode %s meta.json: %w", parsed.ID, err)
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
	owner, _ := meta["owner"].(string)
	if filter.Owner != "" && owner != filter.Owner {
		return false
	}
	if filter.Mine && owner != filter.ActorEmail {
		return false
	}
	return true
}

// Show returns the full state of one task (meta, body, notes).
func (s *Service) Show(ctx context.Context, id string) (*Show, error) {
	state, err := s.writer.Fold(ctx, id)
	if err != nil {
		return nil, err
	}
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

	var notes []Note
	for _, nid := range sortedNoteIDs(state.Collections["notes"]) {
		if n, ok := state.Collections["notes"][nid].(map[string]any); ok {
			notes = append(notes, n)
		}
	}

	return &Show{Meta: metaCopy, Body: body, Notes: notes}, nil
}
