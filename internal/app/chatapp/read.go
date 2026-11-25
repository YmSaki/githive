package chatapp

import (
	"context"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/idresolve"
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
	return entitychain.ListSnapshotMetas(ctx, s.Dir, "refs/projects/chat/", refspace.FeatureChat, func(meta map[string]any) bool {
		if filter.Status != "" {
			if st, _ := meta["status"].(string); st != filter.Status {
				return false
			}
		}
		return true
	})
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
