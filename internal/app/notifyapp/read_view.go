package notifyapp

import (
	"context"
	"errors"

	"github.com/ymsaki/githive/internal/app/snapshotread"
	"github.com/ymsaki/githive/internal/core/chain"
)

// ListView reads a labelled snapshot only when history is intentionally shallow.
func (s *Service) ListView(ctx context.Context, filter ListFilter) ([]Post, string, error) {
	posts, err := s.List(ctx, filter)
	var shallow *chain.ShallowHistoryError
	if !errors.As(err, &shallow) {
		return posts, "", err
	}
	state, err := snapshotread.Read(ctx, s.Dir, StreamRef, "notify")
	if err != nil {
		return nil, "", err
	}
	posts, err = listState(ctx, state, filter)
	return posts, "snapshot", err
}
