// Package syncapp implements `githive sync`: fetch -> merge -> push per ref,
// with retry against non-fast-forward push races
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
package syncapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
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
	// ActionWikiDiverged and ActionWikiSkippedEditing are the two cases
	// where sync deliberately leaves the wiki ref alone: sync only
	// fast-forwards the wiki, it never merges it (ADR-0017).
	ActionWikiDiverged       Action = "wiki-diverged"
	ActionWikiSkippedEditing Action = "wiki-editing"
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

// SupportedFeatures lists every feature sync handles. issue/task/chat
// (per-entity refs), notify (notify/stream and its monthly shards) and users
// (users/registry) go through event-union merge - it works identically for a
// singleton ref, being just union-then-fold like any other. meta/config is
// event-sourced too but has no reducer, so its merge resolves config.json
// directly (metaMergeFiles). wiki is not event-sourced at all (ADR-0003) and
// is fetched and fast-forwarded only, never merged (ADR-0017, planWiki).
//
// cmd/githive's `sync` and `status` commands both use this so the set of
// synced refs stays in one place.
var SupportedFeatures = map[refspace.Feature]bool{
	refspace.FeatureIssue:  true,
	refspace.FeatureTask:   true,
	refspace.FeatureChat:   true,
	refspace.FeatureNotify: true,
	refspace.FeatureUsers:  true,
	refspace.FeatureMeta:   true,
	refspace.FeatureWiki:   true,
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

	// wiki is not event-sourced, so it gets fetch + fast-forward only
	// (ADR-0017); everything below assumes an event chain.
	if parsed.Feature == refspace.FeatureWiki {
		return planWiki(ctx, dir, r, ref, localOID, remoteOID)
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
	// The retryable set is "the remote ref moved or was locked under us",
	// which git words differently depending on where the mismatch was caught
	// and which version is running:
	//
	//   - non-fast-forward / fetch first / stale info: the client noticed
	//     from the advertisement that it is behind.
	//   - failed to update ref / incorrect old value provided: receive-pack's
	//     ref-update transaction found the ref had changed since the
	//     advertisement our push negotiated against - a concurrent push in
	//     the fetch->push window. git 2.43 phrases it the first way, 2.54 the
	//     second, which is why both are listed.
	//   - cannot lock ref: another process held the ref lock; the next round
	//     will not.
	//
	// Everything else stays permanent per A4's rule, so an unrecognized
	// rejection fails immediately rather than burning every retry.
	for _, s := range []string{
		"non-fast-forward", "fetch first", "stale info",
		"failed to update ref", "incorrect old value provided", "cannot lock ref",
	} {
		if strings.Contains(lower, s) {
			return gitx.ErrNonFastForward
		}
	}
	return fmt.Errorf("%w: %s", ErrPushRejected, reason)
}

func mergeAndAdvance(ctx context.Context, dir, ref string, feature refspace.Feature, localOID, remoteOID string) error {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return err
	}
	localHash := plumbing.NewHash(localOID)
	remoteHash := plumbing.NewHash(remoteOID)

	files, err := mergedTree(ctx, dir, repo, feature, localHash, remoteHash)
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

// mergedTree renders the tree an event-union merge commit should carry.
//
// Every feature with a reducer folds the union of both sides' events and
// lets its own TreeFiles render it. meta/config has no reducer - `githive
// init` writes config.json directly (docs/02-data-model.md「meta/config
// ref」) - so its tree is resolved from the two config.json blobs instead
// (ADR-0017). The events themselves still merge as a union and stay in the
// history either way.
func mergedTree(ctx context.Context, dir string, repo *git.Repository, feature refspace.Feature, localHash, remoteHash plumbing.Hash) (map[string][]byte, error) {
	if feature == refspace.FeatureMeta {
		return metaMergeFiles(ctx, dir, localHash.String(), remoteHash.String())
	}
	registry, treeWriter, err := registryAndWriter(feature)
	if err != nil {
		return nil, err
	}
	localEvents, err := chain.WalkChain(repo, localHash)
	if err != nil {
		return nil, err
	}
	remoteEvents, err := chain.WalkChain(repo, remoteHash)
	if err != nil {
		return nil, err
	}
	return treeWriter(merge.Fold(registry, localEvents, remoteEvents))
}

// metaMergeFiles picks the config.json of whichever side declares the higher
// schema_version, local winning a tie.
//
// Higher wins because schema_version is the repository's *minimum* required
// version (docs/01-architecture.md「バージョニングと互換性」) and a tool that
// is too old must refuse to operate. Merging downwards would instead let an
// old tool happily read events written under newer semantics.
func metaMergeFiles(ctx context.Context, dir, localOID, remoteOID string) (map[string][]byte, error) {
	r := gitx.New(dir)
	local, localErr := r.Show(ctx, localOID, metaConfigPath)
	remote, remoteErr := r.Show(ctx, remoteOID, metaConfigPath)

	switch {
	case localErr != nil && remoteErr != nil:
		return nil, fmt.Errorf("syncapp: neither side of %s has %s: %v", refspace.MetaConfigRef, metaConfigPath, localErr)
	case localErr != nil:
		return map[string][]byte{metaConfigPath: remote}, nil
	case remoteErr != nil:
		return map[string][]byte{metaConfigPath: local}, nil
	}

	localVersion, err := configSchemaVersion(local)
	if err != nil {
		return nil, err
	}
	remoteVersion, err := configSchemaVersion(remote)
	if err != nil {
		return nil, err
	}
	if remoteVersion > localVersion {
		return map[string][]byte{metaConfigPath: remote}, nil
	}
	return map[string][]byte{metaConfigPath: local}, nil
}

const metaConfigPath = "config.json"

// configSchemaVersion reads schema_version out of a config.json blob. A
// missing or non-numeric value counts as 0 so a malformed side never wins.
func configSchemaVersion(blob []byte) (int, error) {
	decoded, err := event.DecodeGeneric(blob)
	if err != nil {
		return 0, fmt.Errorf("syncapp: decode %s: %w", metaConfigPath, err)
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("syncapp: %s is not a JSON object", metaConfigPath)
	}
	n, ok := obj["schema_version"]
	if !ok {
		return 0, nil
	}
	switch v := n.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	default:
		// json.Number and anything else: fall back to its text form.
		s := fmt.Sprintf("%v", v)
		parsed := 0
		if _, err := fmt.Sscanf(s, "%d", &parsed); err != nil {
			return 0, nil
		}
		return parsed, nil
	}
}
