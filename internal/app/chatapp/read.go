package chatapp

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ResolveID resolves a full or shortened (>=8 char) ULID to the full ID of
// exactly one existing chat thread.
func (s *Service) ResolveID(ctx context.Context, prefix string) (string, error) {
	id, err := idresolve.Entity(ctx, s.Dir, "chat", prefix)
	if err == idresolve.ErrNotFound {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Service) listIDs(ctx context.Context) ([]string, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/chat/")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureChat {
			continue
		}
		ids = append(ids, parsed.ID)
	}
	return ids, nil
}

// ListFilter narrows List results. Zero value means "no filter".
type ListFilter struct {
	Status string
}

// List returns a snapshot summary of every chat thread, using the fast
// snapshot-read path (meta.json at the ref head).
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Meta, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/chat/")
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
		if err != nil || parsed.Feature != refspace.FeatureChat {
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
			return nil, fmt.Errorf("chatapp: decode %s meta.json: %w", parsed.ID, err)
		}
		meta, ok := decoded.(map[string]any)
		if !ok {
			continue
		}
		if filter.Status != "" {
			if st, _ := meta["status"].(string); st != filter.Status {
				continue
			}
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["id"]) < fmt.Sprint(out[j]["id"])
	})
	return out, nil
}

// Show returns the full state of one chat thread (meta, messages).
func (s *Service) Show(ctx context.Context, id string) (*Show, error) {
	state, err := s.writer.Fold(ctx, id)
	if err != nil {
		return nil, err
	}
	return showState(state)
}

func showState(state *materialize.State) (*Show, error) {
	if state.Meta == nil {
		return nil, ErrNotFound
	}

	var messages []Message
	for _, mid := range sortedMessageIDs(state.Collections["messages"]) {
		if m, ok := state.Collections["messages"][mid].(map[string]any); ok {
			messages = append(messages, m)
		}
	}

	return &Show{Meta: state.Meta, Messages: messages}, nil
}
