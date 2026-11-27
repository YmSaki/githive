package workpackageapp

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wpgraph"
)

// StartOptions configures wp start.
type StartOptions struct {
	Branch string
	// Lease is the claim duration; nil means omitted (DefaultLease). An
	// explicit value must satisfy ValidateLease (0s and sub-millisecond
	// leases are invalid_input, never silently replaced by the default).
	Lease *time.Duration
	// Fetch enables the online CLI's pre-fetch from Remote. The MCP server
	// and --no-sync leave it false (ADR-0011).
	Fetch  bool
	Remote string
}

// StartResult is what Start recorded locally.
type StartResult struct {
	ID      string
	ClaimID string
	// PrefetchErr is the pre-fetch failure that start continued past; the
	// caller reports it as a prefetch_failed warning on success and on any
	// later error alike.
	PrefetchErr error
}

// trackingSnapshot is the remote view fixed right after this start's own
// pre-fetch. It is held immutable for every CAS retry so a concurrent fetch
// that rewinds or prunes refs/githive-remote/* cannot erase what was seen.
type trackingSnapshot struct {
	wps       map[string]string // workpackage id -> tracking tip
	metaOID   string
	fetchedOK bool
}

// prefetch runs the pruned tracking fetch and takes the snapshot from that
// same fetch's result (syncapp.FetchSnapshot, ADR-0035), never by re-reading
// refs/githive-remote/* afterwards.
func (s *Service) prefetch(ctx context.Context, r *gitx.Runner, remote string) (*trackingSnapshot, error) {
	remoteView, err := syncapp.FetchSnapshot(ctx, r, remote)
	if err != nil {
		return nil, err
	}
	if s.afterPrefetch != nil {
		s.afterPrefetch()
	}
	snap := &trackingSnapshot{wps: map[string]string{}, fetchedOK: true, metaOID: remoteView[refspace.MetaConfigRef]}
	for ref, oid := range remoteView {
		if parsed, err := refspace.Parse(ref); err == nil && parsed.Feature == refspace.FeatureWorkpackage {
			snap.wps[parsed.ID] = oid
		}
	}
	return snap, nil
}

// Start claims workpackage idInput: optional pre-fetch, fail-closed history
// and state prechecks over the in-memory union of the local ref and the
// fixed tracking tip, then one local CAS append. It never moves the local
// ref before the claim itself is saved.
func (s *Service) Start(ctx context.Context, idInput string, opts StartOptions) (*StartResult, error) {
	res := &StartResult{}
	lease := DefaultLease
	if opts.Lease != nil {
		lease = *opts.Lease
	}
	// Input boundary: reject a bad lease before any fetch or ID resolution.
	if err := ValidateLease("claim", lease); err != nil {
		return res, err
	}
	r := gitx.New(s.Dir)
	snap := &trackingSnapshot{wps: map[string]string{}}
	if opts.Fetch {
		if err := r.ValidateRemote(ctx, opts.Remote); err != nil {
			return res, err // invalid_remote: never continue locally
		}
		if fixed, err := s.prefetch(ctx, r, opts.Remote); err != nil {
			res.PrefetchErr = err
		} else {
			snap = fixed
		}
	}
	// Common writer checks run before ID resolution (spec §9 step 2-0):
	// local meta/config first, then the shallow-projects gate.
	if err := entitychain.CheckSchemaSupported(ctx, s.Dir); err != nil {
		return res, err
	}
	if err := s.requireComplete(); err != nil {
		return res, err
	}

	localIDs, err := s.localIDs(ctx)
	if err != nil {
		return res, err
	}
	candidates := append([]string(nil), localIDs...)
	for id := range snap.wps {
		candidates = append(candidates, id)
	}
	sort.Strings(candidates)
	id, err := idresolve.Match(idInput, candidates)
	if err == idresolve.ErrNotFound {
		return res, ErrNotFound
	}
	if err != nil {
		return res, err
	}
	res.ID = id
	trackingTip := snap.wps[id]

	hooks := entitychain.AppendHooks{
		Parent: func(localOID string) (string, error) {
			if localOID != gitx.ZeroOID {
				return localOID, nil
			}
			if trackingTip == "" {
				return "", ErrNotFound
			}
			// First adoption: the claim commit sits on the inspected
			// tracking tip and creates the local ref in one CAS from zero.
			return trackingTip, nil
		},
		Check: func(localOID string, env *event.Envelope) error {
			return s.startPrecheck(ctx, id, localOID, trackingTip, snap, env)
		},
	}
	_, err = s.writer.AppendWith(ctx, id, hooks, func(parent string) (*event.Envelope, string, error) {
		env := s.envelope("claim", id, nil)
		until, err := leaseUntil("claim", env.TS, lease)
		if err != nil {
			return nil, "", err
		}
		env.Data = map[string]any{"lease_until": until}
		if opts.Branch != "" {
			env.Data["branch"] = opts.Branch
		}
		res.ClaimID = env.ID
		return env, "workpackage.claim: " + until, nil
	})
	if err != nil {
		res.ClaimID = ""
		return res, err
	}
	return res, nil
}

// startPrecheck runs, in order: tracking meta/config checks, history health
// over local tip + fixed tracking tip + the new claim (ID collisions,
// ref/envelope match, data contracts, create-order per tip and for the
// union), clock skew against the append target, and finally the state
// precheck (spec §9 step 2-3). The shared writer re-checks clock skew.
func (s *Service) startPrecheck(ctx context.Context, id, localOID, trackingTip string, snap *trackingSnapshot, env *event.Envelope) error {
	if snap.fetchedOK && snap.metaOID != "" {
		if err := entitychain.CheckSchemaSupportedAt(ctx, s.Dir, snap.metaOID); err != nil {
			return err
		}
		localMeta, err := gitx.New(s.Dir).RevParse(ctx, refspace.MetaConfigRef)
		if err != nil {
			return err
		}
		if err := syncapp.CheckMetaConfigCompatible(ctx, s.Dir, localMeta, snap.metaOID); err != nil {
			return err
		}
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return err
	}
	var histories [][]*event.Envelope
	for _, tip := range []string{localOID, trackingTip} {
		if tip == "" || tip == gitx.ZeroOID {
			continue
		}
		// Complete-history walk with in-tip ID collision checks; the
		// create-order check runs below, after the cross-tip ID, ref and
		// data-contract checks (spec §9 step 2-1 order).
		events, err := chain.WalkChainSince(repo, plumbing.NewHash(tip), "")
		if err != nil {
			return err
		}
		histories = append(histories, events)
	}
	if len(histories) == 0 {
		return ErrNotFound
	}
	var ids event.IDSet
	var union []*event.Envelope
	for _, h := range append(histories, []*event.Envelope{env}) {
		for _, e := range h {
			fresh, err := ids.Add(e)
			if err != nil {
				return err
			}
			if fresh && e != env {
				union = append(union, e)
			}
		}
	}
	ref := refspace.ParsedRef{Feature: refspace.FeatureWorkpackage, ID: id}
	for _, e := range union {
		if err := refspace.ValidateEventContext(ref, e); err != nil {
			return err
		}
	}
	for _, e := range union {
		if _, err := event.ValidateDataSchema(e); err != nil {
			return err
		}
	}
	for _, h := range histories {
		if err := event.ValidateEntityOrder(h); err != nil {
			return err
		}
	}
	if err := event.ValidateEntityOrder(union); err != nil {
		return err
	}
	// ①-5 clock_skew against the append target only: histories[0] is the
	// local tip, or the tracking tip on first adoption (its claim parent).
	if err := entitychain.ValidateNewEventOrder(env, histories[0]); err != nil {
		return err
	}
	state := materialize.WorkpackageRegistry.Fold(union)
	if state.Meta == nil {
		return ErrNotFound
	}
	if isCancelled(state.Meta) {
		return &ConflictError{Code: "already_cancelled", Message: "workpackage " + id + " is cancelled", Data: map[string]any{}}
	}
	now, err := time.Parse(time.RFC3339Nano, env.TS)
	if err != nil {
		return err
	}
	if active := wpgraph.ActiveClaim(state.Meta, now); active != nil {
		msg := fmt.Sprintf("workpackage %s is claimed by %v until %v", id, active["holder"], active["lease_until"])
		if active["holder"] == env.Actor {
			msg += "; use wp renew to extend your claim"
		}
		return &ConflictError{Code: "claim_held", Message: msg, Data: map[string]any{"active_claim": active}}
	}
	return nil
}

func isCancelled(meta map[string]any) bool {
	c, _ := meta["cancellations"].([]any)
	return len(c) > 0
}

// StartOutcome is a successful start decision.
type StartOutcome struct {
	ClaimID     string
	ActiveClaim map[string]any
}

// Outcome re-reads the actual local ref after any sync and applies the
// exclusive decision table. published is reported back in error data.
func (s *Service) Outcome(ctx context.Context, id, claimID string, published bool) (*StartOutcome, error) {
	state, err := s.fold(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	active := wpgraph.ActiveClaim(state.Meta, now)
	var activeAny any
	if active != nil {
		activeAny = active
	}
	data := map[string]any{"claim_id": claimID, "published": published, "active_claim": activeAny}
	if isCancelled(state.Meta) {
		return nil, &ConflictError{Code: "already_cancelled", Message: "workpackage " + id + " was cancelled", Data: map[string]any{"claim_id": claimID, "published": published}}
	}
	var own map[string]any
	for _, c := range claimList(state.Meta) {
		if c["claim_id"] == claimID {
			own = c
		}
	}
	if own == nil {
		return nil, fmt.Errorf("workpackageapp: claim %s is missing from the local ref", claimID)
	}
	if own["released_at"] != nil {
		return nil, &ConflictError{Code: "claim_released", Message: "claim " + claimID + " was released", Data: data}
	}
	if until, err := time.Parse(time.RFC3339Nano, own["lease_until"].(string)); err == nil && now.After(until) {
		return nil, &ConflictError{Code: "claim_expired", Message: "claim " + claimID + " has expired", Data: data}
	}
	if active != nil && active["claim_id"] == claimID {
		return &StartOutcome{ClaimID: claimID, ActiveClaim: active}, nil
	}
	return nil, &ConflictError{Code: "claim_lost", Message: fmt.Sprintf("claim %s lost to claim %v", claimID, active["claim_id"]), Data: data}
}
