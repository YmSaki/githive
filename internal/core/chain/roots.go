package chain

import (
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// RootCommits returns the OIDs of every zero-parent commit reachable from
// head, sorted for determinism. A healthy chain has exactly one; a chain
// that grew from two independent `githive init`s and was later merged has
// two (docs/03-sync-and-concurrency.md「共通祖先が無い場合」).
func RootCommits(repo *git.Repository, head plumbing.Hash) ([]string, error) {
	var roots []string
	err := walkDFS(repo, head, func(commit *object.Commit) (bool, error) {
		if len(commit.ParentHashes) == 0 {
			roots = append(roots, commit.Hash.String())
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("chain: find root commits: %w", err)
	}
	sort.Strings(roots)
	return roots, nil
}
