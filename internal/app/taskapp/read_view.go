package taskapp

import (
	"context"
	"errors"

	"github.com/ymsaki/githive/internal/app/snapshotread"
	"github.com/ymsaki/githive/internal/core/chain"
)

// ShowView permits explicitly labelled head-tree projections for shallow reads.
// Mutations and resolvers must continue to use Show or their writer's Fold.
func (s *Service) ShowView(ctx context.Context, id string) (*Show, error) {
	shown, err := s.Show(ctx, id)
	var shallow *chain.ShallowHistoryError
	if !errors.As(err, &shallow) {
		return shown, err
	}
	state, err := snapshotread.Read(ctx, s.Dir, refFor(id).String(), "task")
	if err != nil {
		return nil, err
	}
	shown, err = showState(state)
	if err != nil {
		return nil, err
	}
	shown.ReadSource = "snapshot"
	return shown, nil
}

// ListView adds completeness provenance to the existing head-tree list.
func (s *Service) ListView(ctx context.Context, filter ListFilter) ([]Meta, string, error) {
	items, err := s.List(ctx, filter)
	if err != nil {
		return nil, "", err
	}
	source, err := snapshotread.ListSource(ctx, s.Dir, "task")
	return items, source, err
}
