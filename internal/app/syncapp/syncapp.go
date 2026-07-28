// Package syncapp implements `githive sync`: fetch -> merge -> push per ref,
// with retry against non-fast-forward push races
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
package syncapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/merge"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/sign"
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
	// ActionKeptLocalTrustRoot and ActionAdoptedRemoteTrustRoot are the two
	// resolutions of a users/registry with two trust roots, decided by the
	// githive.trust.root pin (docs/03-sync-and-concurrency.md
	// 「共通祖先が無い場合」).
	ActionKeptLocalTrustRoot     Action = "kept-local-trust-root"
	ActionAdoptedRemoteTrustRoot Action = "adopted-remote-trust-root"
)

// Result is the per-ref outcome of a sync. Warning carries a message the
// caller should surface even though the sync itself succeeded (currently
// only trust-root resolutions, which silently pick a winner between two
// registries).
type Result struct {
	Ref     string
	Action  Action
	Warning string
}

// ErrRetriesExhausted is returned (exit code 3 at the CLI layer,
// docs/10-cli-spec.md「終了コード」) when push kept losing to a concurrent
// remote update after Retries attempts. Local state is left as-is; sync can
// simply be re-run.
var ErrRetriesExhausted = errors.New("syncapp: push retries exhausted")

// ErrPushRejected is returned when the remote rejects a push for a reason
// other than a non-fast-forward race - a pre-receive hook, a permission
// error, and so on - so retrying the same push cannot succeed (exit code 1
// at the CLI layer, `error.code` "push_rejected", docs/10-cli-spec.md
// 「終了コード」). Contrast with ErrRetriesExhausted, which means a *real*
// concurrent-update race outlasted the retry budget and simply re-running
// sync is expected to converge.
var ErrPushRejected = errors.New("syncapp: push rejected")

// SupportedFeatures lists the features sync can fetch/merge/push today:
// issue/task/chat (per-entity refs), notify (the single notify/stream ref),
// and users (the single users/registry ref) - event-union merge works
// identically for a singleton ref, since it is just union-then-fold like
// any other ref. wiki is not yet wired up (docs/13-roadmap.md P4: it uses
// ordinary git history, not event-union merge). cmd/githive's `sync` and
// `status` commands both use this so the set of synced refs stays in one
// place.
var SupportedFeatures = map[refspace.Feature]bool{
	refspace.FeatureIssue:  true,
	refspace.FeatureTask:   true,
	refspace.FeatureChat:   true,
	refspace.FeatureNotify: true,
	refspace.FeatureUsers:  true,
}

// registryAndWriter returns the fold registry and tree writer for a
// feature.
func registryAndWriter(feature refspace.Feature) (*materialize.Registry, func(*materialize.State) (map[string][]byte, error), error) {
	switch feature {
	case refspace.FeatureIssue:
		return materialize.IssueRegistry, issueapp.TreeFiles, nil
	case refspace.FeatureTask:
		return materialize.TaskRegistry, taskapp.TreeFiles, nil
	case refspace.FeatureChat:
		return materialize.ChatRegistry, chatapp.TreeFiles, nil
	case refspace.FeatureNotify:
		return materialize.NotifyRegistry, notifyapp.TreeFiles, nil
	case refspace.FeatureUsers:
		return materialize.UsersRegistry, usersapp.TreeFiles, nil
	default:
		return nil, nil, fmt.Errorf("syncapp: feature %q is not yet supported by sync", feature)
	}
}

// Option customizes a Sync run.
type Option func(*syncOptions)

type syncOptions struct {
	skipInitialFetch bool
}

// SkipInitialFetch tells Sync that the caller has already fetched the
// refs/projects/ namespace in this operation - which DiscoverRefs does - so
// the first round can work from what is already in refs/githive-remote/*
// instead of repeating the round trip. Retry rounds still fetch, since a
// retry exists precisely because the remote moved.
func SkipInitialFetch() Option {
	return func(o *syncOptions) { o.skipInitialFetch = true }
}

// Sync runs the fetch/merge/push algorithm for refs against remote.
//
// Work is batched per round rather than per ref
// (docs/03-sync-and-concurrency.md「全 ref 一括の sync は、fetch を 1 回の
// ワイルドカード fetch に、push を複数 refspec の 1 回の git push にまとめる。
// git は ref ごとに受理・拒否を返すため、拒否された ref だけを再試行する」):
// one wildcard fetch, then every ref that needs one is pushed in a single
// `git push` with several refspecs, and only the refs git rejected for a
// racing reason go into the next round. A ref rejected permanently (a
// pre-receive hook, a permission error) drops out immediately and does not
// hold up the others.
//
// Refs that completed are returned in the order they were given, even when
// other refs failed; the error aggregates every per-ref failure
// (docs/01-architecture.md「部分的成功は正常系として報告する」).
func Sync(ctx context.Context, dir, remote string, refs []string, retries int, opts ...Option) ([]Result, error) {
	if retries <= 0 {
		retries = DefaultRetries
	}
	var o syncOptions
	for _, apply := range opts {
		apply(&o)
	}

	r := gitx.New(dir)
	done := make(map[string]Result, len(refs))
	var failures []error

	pending := append([]string(nil), refs...)
	for round := 0; round < retries && len(pending) > 0; round++ {
		if round > 0 || !o.skipInitialFetch {
			if err := fetchNamespace(ctx, r, remote); err != nil {
				return orderedResults(refs, done), errors.Join(append(failures, err)...)
			}
		}

		var toPush []*pushPlan
		for _, ref := range pending {
			plan, res, err := planRef(ctx, dir, r, ref)
			switch {
			case err != nil:
				failures = append(failures, err)
			case plan != nil:
				toPush = append(toPush, plan)
			default:
				done[ref] = res
			}
		}
		if len(toPush) == 0 {
			pending = nil
			break
		}

		retryable, err := pushBatch(ctx, r, remote, toPush, done, &failures)
		if err != nil {
			return orderedResults(refs, done), errors.Join(append(failures, err)...)
		}
		pending = retryable
	}

	if len(pending) > 0 {
		failures = append(failures, fmt.Errorf("%w: %s", ErrRetriesExhausted, strings.Join(pending, ", ")))
	}
	return orderedResults(refs, done), errors.Join(failures...)
}

// pushPlan is a ref that needs pushing, plus what to report once it lands.
type pushPlan struct {
	ref     string
	action  Action
	warning string
}

// fetchNamespace pulls the whole refs/projects/ namespace in one wildcard
// refspec. A wildcard rather than a specific ref because it tolerates the
// remote not having a given ref yet (no error) and covers every ref of the
// round in one round trip.
func fetchNamespace(ctx context.Context, r *gitx.Runner, remote string) error {
	return r.Fetch(ctx, remote, fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot))
}

// planRef decides what one ref needs, doing any purely local work (a
// fast-forward of the local ref, an event-union merge, a trust-root
// resolution) immediately. It returns exactly one of: a plan to push, a
// finished Result, or a permanent error for this ref.
func planRef(ctx context.Context, dir string, r *gitx.Runner, ref string) (*pushPlan, Result, error) {
	trackingRef, err := refspace.RemoteTrackingRef(ref)
	if err != nil {
		return nil, Result{}, err
	}
	parsed, err := refspace.Parse(ref)
	if err != nil {
		return nil, Result{}, err
	}
	localOID, err := r.RevParse(ctx, ref)
	if err != nil {
		return nil, Result{}, err
	}
	remoteOID, err := r.RevParse(ctx, trackingRef)
	if err != nil {
		return nil, Result{}, err
	}

	if localOID == remoteOID {
		return nil, Result{Ref: ref, Action: ActionUpToDate}, nil
	}

	if remoteOID == "" {
		// Remote has never seen this ref: plain push.
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

	// Diverged. For users/registry only, a divergence with no common
	// ancestor is not an ordinary conflict but two competing trust roots,
	// which must never be merged automatically
	// (docs/03-sync-and-concurrency.md「共通祖先が無い場合」).
	if parsed.Feature == refspace.FeatureUsers {
		res, handled, err := resolveTrustRoots(ctx, dir, r, ref, localOID, remoteOID)
		if err != nil {
			return nil, Result{}, err
		}
		if handled {
			return nil, res, nil
		}
	}

	// event-union merge (docs/03-sync-and-concurrency.md「event-union マージ」),
	// then CAS-advance local; the push happens with the rest of the batch.
	if err := mergeAndAdvance(ctx, dir, ref, parsed.Feature, localOID, remoteOID); err != nil {
		return nil, Result{}, err
	}
	return &pushPlan{ref: ref, action: ActionMerged}, Result{}, nil
}

// pushBatch pushes every planned ref in one `git push`, records the ones that
// landed in done, appends permanent rejections to failures, and returns the
// refs whose rejection a further fetch->merge->push round can resolve.
func pushBatch(ctx context.Context, r *gitx.Runner, remote string, plans []*pushPlan, done map[string]Result, failures *[]error) ([]string, error) {
	refspecs := make([]string, len(plans))
	for i, p := range plans {
		refspecs[i] = fmt.Sprintf("%s:%s", p.ref, p.ref)
	}
	results, err := r.Push(ctx, remote, refspecs...)
	if err != nil {
		return nil, err
	}

	byRef := make(map[string]gitx.PushResult, len(results))
	for _, res := range results {
		byRef[localRefOfRefspec(res.Refspec)] = res
	}

	var retryable []string
	for _, p := range plans {
		res, ok := byRef[p.ref]
		switch {
		case !ok:
			// git reported nothing for this refspec. Rather than assume it
			// succeeded, treat an unexplained silence as a permanent failure
			// - the safe side, per A4's rule that an unclassifiable rejection
			// must not be retried forever.
			*failures = append(*failures, fmt.Errorf("%w: %s: git push reported no result for this ref", ErrPushRejected, p.ref))
		case res.OK:
			done[p.ref] = Result{Ref: p.ref, Action: p.action, Warning: p.warning}
		default:
			if rejection := classifyPushRejection(res.Reason); errors.Is(rejection, gitx.ErrNonFastForward) {
				retryable = append(retryable, p.ref)
			} else {
				*failures = append(*failures, fmt.Errorf("%s: %w", p.ref, rejection))
			}
		}
	}
	return retryable, nil
}

// localRefOfRefspec extracts the local side of a `<from>:<to>` refspec as
// `git push --porcelain` echoes it back.
func localRefOfRefspec(refspec string) string {
	if from, _, ok := strings.Cut(refspec, ":"); ok {
		return from
	}
	return refspec
}

// orderedResults returns the finished results in the order refs were given,
// skipping refs that did not complete.
func orderedResults(refs []string, done map[string]Result) []Result {
	out := make([]Result, 0, len(done))
	for _, ref := range refs {
		if res, ok := done[ref]; ok {
			out = append(out, res)
		}
	}
	return out
}

// classifyPushRejection maps the reason field of a rejected `git push
// --porcelain` refspec (its second tab-separated field, e.g.
// "[rejected] (non-fast-forward)") to gitx.ErrNonFastForward for races a
// fetch->merge->push retry can resolve (non-fast-forward / fetch-first /
// stale-info), or to ErrPushRejected - carrying reason verbatim - for every
// other rejection (pre-receive hook, permission denied, ...), which no
// amount of retrying fixes. Git's exact wording can vary by version, so this
// is a best-effort substring match; per docs/10-cli-spec.md, a rejection
// reason it does not recognize is deliberately classified as ErrPushRejected
// (permanent) rather than ErrNonFastForward (infinitely retried) - an
// unnecessary immediate failure is safer than a retry loop that can never
// succeed.
func classifyPushRejection(reason string) error {
	lower := strings.ToLower(reason)
	// "failed to update ref" is receive-pack's answer when the ref moved
	// between the advertisement our push negotiated against and the update it
	// tried to apply - i.e. another client pushed in the window. It reads
	// like a permanent failure but is exactly the race a fetch->merge->push
	// retry resolves, and it is what a genuine concurrent push produces over
	// the local transport (where the client never sees a stale-enough
	// advertisement to call it non-fast-forward itself).
	for _, s := range []string{"non-fast-forward", "fetch first", "stale info", "failed to update ref"} {
		if strings.Contains(lower, s) {
			return gitx.ErrNonFastForward
		}
	}
	return fmt.Errorf("%w: %s", ErrPushRejected, reason)
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
	signer, cleanupSigner, err := sign.ResolveSigner(ctx, dir)
	if err != nil {
		return err
	}
	defer cleanupSigner()
	mergeHash, err := chain.AppendMerge(repo, [2]plumbing.Hash{localHash, remoteHash}, files, sig, chain.WithSigner(signer))
	if err != nil {
		return err
	}
	return chain.AdvanceRef(dir, plumbing.ReferenceName(ref), mergeHash, localOID)
}
