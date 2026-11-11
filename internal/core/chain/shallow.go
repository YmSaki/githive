package chain

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ShallowHistoryError distinguishes intentionally omitted history from
// corruption. No remote is selected or contacted by this check.
type ShallowHistoryError struct{ Head plumbing.Hash }

func (e *ShallowHistoryError) Error() string {
	return fmt.Sprintf("shallow_history: project history at %s is incomplete; fetch the affected refs/projects/* with --unshallow (or --deepen) from the configured remote you intend to use, then retry", e.Head)
}

// RequireCompleteHistory checks only shallow boundaries reachable from head.
// A shallow source branch does not prevent using complete project refs.
func RequireCompleteHistory(repo *git.Repository, head plumbing.Hash) error {
	boundaries, err := repo.Storer.Shallow()
	if err != nil {
		return err
	}
	if len(boundaries) == 0 || head == plumbing.ZeroHash {
		return nil
	}
	shallow := map[plumbing.Hash]bool{}
	for _, h := range boundaries {
		shallow[h] = true
	}
	return walkDFS(repo, head, func(c *object.Commit) (bool, error) {
		if shallow[c.Hash] && len(c.ParentHashes) > 0 {
			return false, &ShallowHistoryError{Head: head}
		}
		return true, nil
	})
}

// RequireCompleteProjects protects mutations from depending on a shallow
// registry or another incomplete project ref, including when creating a new ref.
func RequireCompleteProjects(repo *git.Repository) error {
	boundaries, err := repo.Storer.Shallow()
	if err != nil {
		return err
	}
	if len(boundaries) == 0 {
		return nil
	}
	refs, err := repo.References()
	if err != nil {
		return err
	}
	defer refs.Close()
	return refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference && strings.HasPrefix(ref.Name().String(), "refs/projects/") {
			return RequireCompleteHistory(repo, ref.Hash())
		}
		return nil
	})
}
