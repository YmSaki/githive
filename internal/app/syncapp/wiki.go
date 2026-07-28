package syncapp

import (
	"context"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/wikiapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// planWiki is sync's wiki branch. Unlike every other feature, wiki is not
// event-sourced (ADR-0003), so there is no event union to merge: sync only
// fetches, fast-forwards, and reports (ADR-0017).
//
// Three outcomes, none of which touch working files:
//   - fast-forward in either direction is applied (or pushed with the batch);
//   - a genuine divergence is reported as a warning telling the operator to
//     resolve it with `githive wiki edit` + `githive wiki save`, which owns
//     the 3-way merge and its conflict flow;
//   - an edit already in progress skips the ref entirely, so the ref cannot
//     move under a checked-out worktree.
func planWiki(ctx context.Context, dir string, r *gitx.Runner, ref, localOID, remoteOID string) (*pushPlan, Result, error) {
	editing, worktree, err := wikiEditInProgress(ctx, r)
	if err != nil {
		return nil, Result{}, err
	}
	if editing {
		return nil, Result{
			Ref:     ref,
			Action:  ActionWikiSkippedEditing,
			Warning: fmt.Sprintf("%s: a wiki edit is in progress at %s; run `githive wiki save` to publish it, then sync again", ref, worktree),
		}, nil
	}

	if remoteOID == "" {
		return &pushPlan{ref: ref, action: ActionFastForwardOut}, Result{}, nil
	}
	if localOID == "" {
		if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), gitx.ZeroOID); err != nil {
			return nil, Result{}, err
		}
		return nil, Result{Ref: ref, Action: ActionFastForwardIn}, nil
	}

	localIsAncestor, err := r.IsAncestor(ctx, localOID, remoteOID)
	if err != nil {
		return nil, Result{}, err
	}
	if localIsAncestor {
		if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), localOID); err != nil {
			return nil, Result{}, err
		}
		return nil, Result{Ref: ref, Action: ActionFastForwardIn}, nil
	}

	remoteIsAncestor, err := r.IsAncestor(ctx, remoteOID, localOID)
	if err != nil {
		return nil, Result{}, err
	}
	if remoteIsAncestor {
		return &pushPlan{ref: ref, action: ActionFastForwardOut}, Result{}, nil
	}

	return nil, Result{
		Ref:     ref,
		Action:  ActionWikiDiverged,
		Warning: fmt.Sprintf("%s: local and remote wiki have diverged; sync does not merge the wiki - run `githive wiki edit` and `githive wiki save` to resolve it", ref),
	}, nil
}

// wikiEditInProgress reports whether githive.wiki.worktree names a worktree
// that is still on disk. A recorded worktree the operator deleted by hand is
// treated as no edit in progress, matching wikiapp.Edit's own recovery.
func wikiEditInProgress(ctx context.Context, r *gitx.Runner) (bool, string, error) {
	worktree, err := r.ConfigGet(ctx, wikiapp.WorktreeConfigKey)
	if err != nil {
		return false, "", err
	}
	if worktree == "" {
		return false, "", nil
	}
	if _, err := os.Stat(worktree); err != nil {
		return false, "", nil
	}
	return true, worktree, nil
}
