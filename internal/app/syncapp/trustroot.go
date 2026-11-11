package syncapp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
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
//
// Grafted marks the other shape of the same problem: the two sides do share
// history, but the remote's history contains a root commit that is neither
// one of the local roots nor the pin - an unrelated registry merged in
// underneath a legitimate head (#113). A fast-forward or ordinary merge
// would adopt it, so sync refuses that too.
type TrustRootConflictError struct {
	Ref         string
	LocalRoots  []string
	RemoteRoots []string
	Pin         string
	Grafted     bool
}

func (e *TrustRootConflictError) Error() string {
	if e.Grafted {
		return fmt.Sprintf("syncapp: %s on the remote contains root commit(s) not in the local registry or %s (local root(s) %s, remote root(s) %s, pin %q); refusing to adopt a registry grafted onto an unrelated root",
			e.Ref, TrustRootPinKey, strings.Join(e.LocalRoots, ", "), strings.Join(e.RemoteRoots, ", "), e.Pin)
	}
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
	if e.Grafted {
		return fmt.Sprintf("inspect the remote registry with `git log --graph %s` and `githive verify`; do not sync it until the extra root is explained", RemoteTrackingRefOrSelf(e.Ref))
	}
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
			Ref:      ref,
			Action:   ActionKeptLocalTrustRoot,
			LocalOID: localOID, RemoteOID: remoteOID,
			Recovery: "The local registry is preserved but not published; inspect both trust roots and choose the intended registry before syncing again.",
			Warning: fmt.Sprintf("%s: the remote has an unrelated registry (root %s); kept the local one, which matches %s. The remote's entries were not merged in.",
				ref, remoteRoots[0], TrustRootPinKey),
		}, true, nil
	case remoteRoots[0]:
		backupRef, err := preserveRegistryTip(ctx, r, localOID)
		if err != nil {
			return Result{}, false, err
		}
		if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), plumbing.NewHash(remoteOID), localOID); err != nil {
			return Result{}, false, err
		}
		return Result{
			Ref:      ref,
			Action:   ActionAdoptedRemoteTrustRoot,
			LocalOID: localOID, RemoteOID: remoteOID, BackupRef: backupRef,
			Recovery: fmt.Sprintf("The previous local tip %s is durably preserved at %s. Inspect it with `git show %s`; create a separate inspection branch with `git branch githive-recovered-users-%s %s`. Neither command overwrites the current registry or an existing branch. Reconcile trust roots explicitly; do not blindly retry the original write.", localOID, backupRef, backupRef, localOID[:12], backupRef),
			Warning: fmt.Sprintf("%s: adopted the remote registry (root %s) per %s; the previous local tip (%s) is no longer current. Use the concrete recovery instructions; an existing reflog is left intact.",
				ref, remoteRoots[0], TrustRootPinKey, localOID),
		}, true, nil
	default:
		return Result{}, false, conflict
	}
}

// preserveRegistryTip creates a local-only, GC-durable recovery root before
// replacing a registry. It never overwrites an existing recovery ref.
func preserveRegistryTip(ctx context.Context, r *gitx.Runner, oid string) (string, error) {
	ref := "refs/githive-recovery/users/" + oid
	existing, err := r.RevParse(ctx, ref)
	if err != nil {
		return "", err
	}
	if existing == oid {
		return ref, nil
	}
	if existing != "" {
		return "", fmt.Errorf("syncapp: recovery ref %s points at %s instead of %s; refusing to replace the local registry", ref, existing, oid)
	}
	if err := r.UpdateRef(ctx, ref, oid, gitx.ZeroOID); err != nil {
		return "", fmt.Errorf("syncapp: cannot preserve local registry at %s; refusing remote adoption: %w", ref, err)
	}
	return ref, nil
}

// RemoteTrackingRefOrSelf is the remote-tracking ref for ref, or ref itself
// if it has none (used only in operator-facing messages).
func RemoteTrackingRefOrSelf(ref string) string {
	if tr, err := refspace.RemoteTrackingRef(ref); err == nil {
		return tr
	}
	return ref
}

// checkIncomingRegistryRoots refuses to bring remoteOID into the local
// users/registry when the remote's history contains a root commit the local
// side doesn't already trust (docs/11-security.md「信頼の根」, #113). The
// trusted roots are the pin when set, otherwise the local roots; with no
// local registry and no pin, a single remote root is accepted on first use
// (TOFU) and more than one is refused. It only runs on paths that share
// history (or have no local side); the no-merge-base case is
// resolveTrustRoots' job.
func checkIncomingRegistryRoots(ctx context.Context, dir string, r *gitx.Runner, ref, localOID, remoteOID string) error {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return err
	}
	remoteRoots, err := chain.RootCommits(repo, plumbing.NewHash(remoteOID))
	if err != nil {
		return err
	}
	var localRoots []string
	if localOID != "" {
		if localRoots, err = chain.RootCommits(repo, plumbing.NewHash(localOID)); err != nil {
			return err
		}
	}
	pin, err := r.ConfigGet(ctx, TrustRootPinKey)
	if err != nil {
		return err
	}

	trusted := localRoots
	if pin != "" {
		trusted = []string{pin}
	}
	if len(trusted) == 0 {
		if len(remoteRoots) <= 1 {
			return nil
		}
	} else {
		ok := true
		for _, root := range remoteRoots {
			if !slices.Contains(trusted, root) {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
	}
	return &TrustRootConflictError{Ref: ref, LocalRoots: localRoots, RemoteRoots: remoteRoots, Pin: pin, Grafted: true}
}
