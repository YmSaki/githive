package syncapp

import (
	"context"
	"fmt"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// DiscoverRefs enumerates every ref `githive sync` should process, optionally
// restricted to kinds (feature names; empty means all supported features).
//
// **It performs network I/O**: a wildcard fetch of the whole refs/projects/
// namespace runs first. That is not an optimization but a correctness
// requirement - an entity another clone created (an issue, task, chat thread
// or notify post this clone has never seen) exists only in the remote at
// this point. Enumerating local refs alone would make `githive sync` unable
// to ever discover or fast-forward in anything created elsewhere: it would
// silently re-confirm "up-to-date" for the refs it already knew and do
// nothing (docs/03-sync-and-concurrency.md「sync のアルゴリズム」treats 対象
// ref as the full working set, not just the already-known ones).
//
// So the sequence is: fetch the namespace, then union the local refs with
// whatever showed up in the remote-tracking namespace (refs/githive-remote/*,
// ADR-0008).
func DiscoverRefs(ctx context.Context, dir, remote string, kinds []string) ([]string, error) {
	r := gitx.New(dir)
	if err := r.Fetch(ctx, remote, fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)); err != nil {
		return nil, err
	}

	localEntries, err := r.ForEachRef(ctx, refspace.Root+"/")
	if err != nil {
		return nil, err
	}
	trackingEntries, err := r.ForEachRef(ctx, refspace.RemoteTrackingRoot+"/")
	if err != nil {
		return nil, err
	}

	allowed := map[string]bool{}
	for _, k := range kinds {
		allowed[k] = true
	}

	seen := map[string]bool{}
	var refs []string
	add := func(ref string) {
		parsed, err := refspace.Parse(ref)
		if err != nil || !SupportedFeatures[parsed.Feature] {
			return
		}
		if len(allowed) > 0 && !allowed[string(parsed.Feature)] {
			return
		}
		if seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}

	for _, e := range localEntries {
		add(e.Ref)
	}
	for _, e := range trackingEntries {
		localRef, err := refspace.LocalRefFromTracking(e.Ref)
		if err != nil {
			continue
		}
		add(localRef)
	}
	return refs, nil
}

// UnpushedRefs lists the local refs whose last-known remote-tracking ref
// disagrees with them, i.e. what `githive status` reports as not yet pushed.
//
// Unlike DiscoverRefs it does **not** fetch: status is a fast, network-free
// local summary (docs/01-architecture.md「読み取り（高速路と互換路）」), so
// its answer is relative to the last fetch rather than to the remote's
// current state.
func UnpushedRefs(ctx context.Context, dir string) ([]string, error) {
	r := gitx.New(dir)
	entries, err := r.ForEachRef(ctx, refspace.Root+"/")
	if err != nil {
		return nil, err
	}
	var unpushed []string
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || !SupportedFeatures[parsed.Feature] {
			continue
		}
		trackingRef, err := refspace.RemoteTrackingRef(e.Ref)
		if err != nil {
			continue
		}
		trackingOID, err := r.RevParse(ctx, trackingRef)
		if err != nil {
			return nil, err
		}
		if trackingOID != e.OID {
			unpushed = append(unpushed, e.Ref)
		}
	}
	return unpushed, nil
}
