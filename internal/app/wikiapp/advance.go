package wikiapp

import (
	"context"
	"fmt"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// LocalAdvanceConflictError means a local writer advanced the wiki outside the
// editor's history. Both tips and the recorded worktree remain available.
type LocalAdvanceConflictError struct {
	Current  string
	Head     string
	Worktree string
}

func (e *LocalAdvanceConflictError) Error() string {
	return fmt.Sprintf("wikiapp: local wiki %s is not an ancestor of saved edit %s; both histories are retained; merge %s into the worktree at %q, resolve any conflicts, then re-run githive wiki save", e.Current, e.Head, refspace.WikiMainRef, e.Worktree)
}

// advanceWikiRef permits only a fast-forward from the exact tip we inspected.
// A concurrent change after the ancestor check is rejected by UpdateRef's CAS.
func advanceWikiRef(ctx context.Context, r *gitx.Runner, worktreeDir, head string) error {
	current, err := r.RevParse(ctx, refspace.WikiMainRef)
	if err != nil {
		return err
	}
	if current != "" {
		ff, err := r.IsAncestor(ctx, current, head)
		if err != nil {
			return err
		}
		if !ff {
			return &LocalAdvanceConflictError{Current: current, Head: head, Worktree: worktreeDir}
		}
	}
	return r.UpdateRef(ctx, refspace.WikiMainRef, head, orZero(current))
}
