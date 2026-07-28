package syncapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// TrustRootPinKey is the git config key holding the OID of the users
// registry root commit this repository trusts (docs/11-security.md
// 「信頼の根」). verifyapp checks the same key after the fact; sync uses it
// to decide which of two competing registries wins.
const TrustRootPinKey = "githive.trust.root"

// TrustRootConflictError means the local and remote users/registry share no
// commit at all - two independently created registries - and
// githive.trust.root does not tell us which one to believe. Merging them
// would silently union two identity ledgers, so sync refuses and leaves the
// local ref untouched (docs/03-sync-and-concurrency.md「共通祖先が無い場合」).
type TrustRootConflictError struct {
	Ref         string
	LocalRoots  []string
	RemoteRoots []string
	Pin         string
}

func (e *TrustRootConflictError) Error() string {
	detail := fmt.Sprintf("local root(s) %s, remote root(s) %s",
		strings.Join(e.LocalRoots, ", "), strings.Join(e.RemoteRoots, ", "))
	if e.Pin == "" {
		return fmt.Sprintf("syncapp: %s has two unrelated trust roots and %s is not set (%s); pin the root OID you trust with `git config %s <oid>` and re-run",
			e.Ref, TrustRootPinKey, detail, TrustRootPinKey)
	}
	return fmt.Sprintf("syncapp: %s has two unrelated trust roots and %s=%s matches neither (%s); pin the root OID you trust and re-run",
		e.Ref, TrustRootPinKey, e.Pin, detail)
}

// Recovery is the operator-facing next step, surfaced in the CLI's JSON
// error data so an agent can act on it without reading docs.
func (e *TrustRootConflictError) Recovery() string {
	return fmt.Sprintf("git config %s <trusted root oid> && githive sync", TrustRootPinKey)
}

// resolveTrustRoots handles the users/registry special case. It reports
// handled=false when the two sides do share history, in which case the
// caller proceeds with the ordinary event-union merge.
//
// Two roots are detected by the absence of a merge base, not by counting
// zero-parent commits: a registry that legitimately merged two histories in
// the past contains several roots yet still shares commits with its remote
// (docs/03-sync-and-concurrency.md「共通祖先が無い場合」).
func resolveTrustRoots(ctx context.Context, dir string, r *gitx.Runner, ref, localOID, remoteOID string) (Result, bool, error) {
	if _, err := r.MergeBase(ctx, localOID, remoteOID); err == nil {
		return Result{}, false, nil
	} else if !errors.Is(err, gitx.ErrNoMergeBase) {
		return Result{}, false, err
	}

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return Result{}, false, err
	}
	localRoots, err := chain.RootCommits(repo, plumbing.NewHash(localOID))
	if err != nil {
		return Result{}, false, err
	}
	remoteRoots, err := chain.RootCommits(repo, plumbing.NewHash(remoteOID))
	if err != nil {
		return Result{}, false, err
	}
	pin, err := r.ConfigGet(ctx, TrustRootPinKey)
	if err != nil {
		return Result{}, false, err
	}

	conflict := &TrustRootConflictError{Ref: ref, LocalRoots: localRoots, RemoteRoots: remoteRoots, Pin: pin}
	if pin == "" || len(localRoots) != 1 || len(remoteRoots) != 1 {
		// More than one root on a side is an already-broken shape; refusing
		// is safer than guessing which of them the pin was meant to match.
		return Result{}, false, conflict
	}

	switch pin {
	case localRoots[0]:
		return Result{
			Ref:    ref,
			Action: ActionKeptLocalTrustRoot,
			Warning: fmt.Sprintf("%s: the remote has an unrelated registry (root %s); kept the local one, which matches %s. The remote's entries were not merged in.",
				ref, remoteRoots[0], TrustRootPinKey),
		}, true, nil
	case remoteRoots[0]:
		if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), localOID); err != nil {
			return Result{}, false, err
		}
		return Result{
			Ref:    ref,
			Action: ActionAdoptedRemoteTrustRoot,
			Warning: fmt.Sprintf("%s: adopted the remote registry (root %s) per %s; the discarded local chain (%s) is still reachable through this ref's reflog.",
				ref, remoteRoots[0], TrustRootPinKey, localOID),
		}, true, nil
	default:
		return Result{}, false, conflict
	}
}
