// Package merge implements event-union merging: the mechanism by which two
// diverged commit chains converge without manual conflict resolution
// (docs/03-sync-and-concurrency.md「event-union マージ」, ADR-0004).
//
// Because fold (internal/core/materialize) is a pure function of an event
// set, merging is simply: union the two sides' event sets (by ID, so
// duplicates from shared ancestry collapse for free) and fold the result.
// No common-ancestor search is needed - shared history just contributes the
// same events on both sides, which the union already dedupes.
package merge

import (
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// UnionEvents merges any number of event sets into one, deduplicated by ID.
// Order of the input sets does not affect the result (commutative,
// associative, idempotent - docs/14-testing.md「マージの収束」).
func UnionEvents(sets ...[]*event.Envelope) []*event.Envelope {
	byID := map[string]*event.Envelope{}
	var order []string
	for _, set := range sets {
		for _, env := range set {
			if _, seen := byID[env.ID]; !seen {
				order = append(order, env.ID)
			}
			byID[env.ID] = env
		}
	}
	sort.Strings(order)
	out := make([]*event.Envelope, len(order))
	for i, id := range order {
		out[i] = byID[id]
	}
	return out
}

// Fold unions the given event sets and folds them with registry. This is
// the pure, git-independent core of a merge.
func Fold(registry *materialize.Registry, sets ...[]*event.Envelope) *materialize.State {
	return registry.Fold(UnionEvents(sets...))
}

// TreeFiles renders a fold State into a generic tree layout: meta.json plus
// one canonical-JSON file per collection item under
// "<collection>/<event-id>.json". Real features (P1) use their own writers
// with feature-specific formats (front matter, markdown bodies, etc,
// docs/02-data-model.md「実体化ツリーの共通配置」); this generic form exists
// so core/merge can build and test merge commits without depending on any
// particular feature.
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	files := map[string][]byte{}
	if state.Meta != nil {
		b, err := event.Encode(map[string]any(state.Meta))
		if err != nil {
			return nil, err
		}
		files["meta.json"] = b
	}
	for collection, items := range state.Collections {
		for id, item := range items {
			b, err := event.Encode(item)
			if err != nil {
				return nil, err
			}
			files[collection+"/"+id+".json"] = b
		}
	}
	return files, nil
}

// Chains merges two chain heads: it walks both sides' full event history,
// unions and folds it, and creates a two-parent merge commit
// (message "merge: event-union", docs/02-data-model.md「コミットの規約」)
// whose tree is the fold result rendered via TreeFiles. It does not move any
// ref; the caller CAS-updates the ref to the returned hash
// (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」).
func Chains(repo *git.Repository, registry *materialize.Registry, parents [2]plumbing.Hash, sig chain.Signature) (plumbing.Hash, error) {
	eventsA, err := chain.WalkChain(repo, parents[0])
	if err != nil {
		return plumbing.ZeroHash, err
	}
	eventsB, err := chain.WalkChain(repo, parents[1])
	if err != nil {
		return plumbing.ZeroHash, err
	}
	state := Fold(registry, eventsA, eventsB)
	files, err := TreeFiles(state)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return chain.AppendMerge(repo, parents, files, sig)
}
