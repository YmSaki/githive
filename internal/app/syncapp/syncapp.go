// Package syncapp implements `githive sync`: fetch -> merge -> push per ref,
// with retry against non-fast-forward push races
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
package syncapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/merge"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// DefaultRetries matches githive.sync.retries' default (docs/10-cli-spec.md).
const DefaultRetries = 5

// Action classifies what Sync did for one ref.
type Action string

const (
	ActionUpToDate       Action = "up-to-date"
	ActionFastForwardIn  Action = "fast-forward-local"
	ActionFastForwardOut Action = "fast-forward-push"
	ActionMerged         Action = "merged"
)

// Result is the per-ref outcome of a sync.
type Result struct {
	Ref    string
	Action Action
}

// ErrRetriesExhausted is returned (exit code 3 at the CLI layer,
// docs/10-cli-spec.md「終了コード」) when push kept losing to a concurrent
// remote update after Retries attempts. Local state is left as-is; sync can
// simply be re-run.
var ErrRetriesExhausted = errors.New("syncapp: push retries exhausted")

// registryAndWriter returns the fold registry and tree writer for a
// feature. issue/task/chat are per-entity refs sync can merge directly;
// notify (a singleton stream) and users/wiki are not yet wired up here
// (docs/13-roadmap.md P2/P3).
func registryAndWriter(feature refspace.Feature) (*materialize.Registry, func(*materialize.State) (map[string][]byte, error), error) {
	switch feature {
	case refspace.FeatureIssue:
		return materialize.IssueRegistry, issueapp.TreeFiles, nil
	case refspace.FeatureTask:
		return materialize.TaskRegistry, taskapp.TreeFiles, nil
	case refspace.FeatureChat:
		return materialize.ChatRegistry, chatapp.TreeFiles, nil
	default:
		return nil, nil, fmt.Errorf("syncapp: feature %q is not yet supported by sync", feature)
	}
}

// Sync runs the fetch/merge/push algorithm for every ref in refs, against
// remote, retrying rejected pushes up to retries times.
func Sync(ctx context.Context, dir, remote string, refs []string, retries int) ([]Result, error) {
	if retries <= 0 {
		retries = DefaultRetries
	}
	results := make([]Result, 0, len(refs))
	for _, ref := range refs {
		res, err := syncOne(ctx, dir, remote, ref, retries)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

func syncOne(ctx context.Context, dir, remote, ref string, retries int) (Result, error) {
	r := gitx.New(dir)
	trackingRef, err := refspace.RemoteTrackingRef(ref)
	if err != nil {
		return Result{}, err
	}
	parsed, err := refspace.Parse(ref)
	if err != nil {
		return Result{}, err
	}

	for attempt := 0; attempt < retries; attempt++ {
		// Fetch the whole refs/projects/ namespace as one wildcard
		// refspec rather than this single ref: unlike a specific-ref
		// refspec, a wildcard tolerates the remote not having this ref yet
		// (no error), and batches all refs into one round trip when Sync
		// is called for many refs
		// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
		if err := r.Fetch(ctx, remote, fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)); err != nil {
			return Result{}, err
		}
		localOID, err := r.RevParse(ctx, ref)
		if err != nil {
			return Result{}, err
		}
		remoteOID, err := r.RevParse(ctx, trackingRef)
		if err != nil {
			return Result{}, err
		}

		if localOID == remoteOID {
			return Result{Ref: ref, Action: ActionUpToDate}, nil
		}

		if remoteOID == "" {
			// Remote has never seen this ref: plain push.
			ok, err := push(ctx, r, remote, ref)
			if err != nil {
				return Result{}, err
			}
			if ok {
				return Result{Ref: ref, Action: ActionFastForwardOut}, nil
			}
			continue // remote moved between fetch and push; retry
		}

		if localOID == "" {
			if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), gitx.ZeroOID); err != nil {
				return Result{}, err
			}
			return Result{Ref: ref, Action: ActionFastForwardIn}, nil
		}

		localIsAncestor, err := r.IsAncestor(ctx, localOID, remoteOID)
		if err != nil {
			return Result{}, err
		}
		if localIsAncestor {
			if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), localOID); err != nil {
				return Result{}, err
			}
			return Result{Ref: ref, Action: ActionFastForwardIn}, nil
		}

		remoteIsAncestor, err := r.IsAncestor(ctx, remoteOID, localOID)
		if err != nil {
			return Result{}, err
		}
		if remoteIsAncestor {
			ok, err := push(ctx, r, remote, ref)
			if err != nil {
				return Result{}, err
			}
			if ok {
				return Result{Ref: ref, Action: ActionFastForwardOut}, nil
			}
			continue
		}

		// Diverged: event-union merge (docs/03-sync-and-concurrency.md
		// 「event-union マージ」), then CAS-advance local and push.
		if err := mergeAndAdvance(ctx, dir, ref, parsed.Feature, localOID, remoteOID); err != nil {
			return Result{}, err
		}
		ok, err := push(ctx, r, remote, ref)
		if err != nil {
			return Result{}, err
		}
		if ok {
			return Result{Ref: ref, Action: ActionMerged}, nil
		}
		// Remote advanced again during our merge; refetch and redo.
	}
	return Result{}, ErrRetriesExhausted
}

func push(ctx context.Context, r *gitx.Runner, remote, ref string) (bool, error) {
	results, err := r.Push(ctx, remote, fmt.Sprintf("%s:%s", ref, ref))
	if err != nil {
		return false, err
	}
	for _, res := range results {
		if res.OK {
			return true, nil
		}
	}
	return false, nil
}

func mergeAndAdvance(ctx context.Context, dir, ref string, feature refspace.Feature, localOID, remoteOID string) error {
	registry, treeWriter, err := registryAndWriter(feature)
	if err != nil {
		return err
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return err
	}
	localHash := plumbing.NewHash(localOID)
	remoteHash := plumbing.NewHash(remoteOID)

	localEvents, err := chain.WalkChain(repo, localHash)
	if err != nil {
		return err
	}
	remoteEvents, err := chain.WalkChain(repo, remoteHash)
	if err != nil {
		return err
	}
	state := merge.Fold(registry, localEvents, remoteEvents)
	files, err := treeWriter(state)
	if err != nil {
		return err
	}
	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		return err
	}
	mergeHash, err := chain.AppendMerge(repo, [2]plumbing.Hash{localHash, remoteHash}, files, sig)
	if err != nil {
		return err
	}
	return chain.AdvanceRef(dir, plumbing.ReferenceName(ref), mergeHash, localOID)
}
