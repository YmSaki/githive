package syncapp

import (
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
)

// validateEventIDs checks both histories before adopting, pushing, or merging
// either tip. Even a fast-forward or a meta/config merge must reject collisions.
func validateEventIDs(dir string, oids ...string) error {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return err
	}
	var ids event.IDSet
	seen := map[string]bool{}
	for _, oid := range oids {
		if oid == "" || seen[oid] {
			continue
		}
		seen[oid] = true
		events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
		if err != nil {
			return err
		}
		for _, env := range events {
			if _, err := ids.Add(env); err != nil {
				return err
			}
		}
	}
	return nil
}
