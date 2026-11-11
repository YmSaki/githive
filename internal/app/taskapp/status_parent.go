package taskapp

import (
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// statusAtParent reads the immutable head supplied by AppendOnto. The
// callback is rerun after a lost CAS, so audit metadata describes the state
// actually observed at the successful append's parent, not an earlier read.
func (s *Service) statusAtParent(parentOID string) (string, error) {
	if parentOID == gitx.ZeroOID {
		return "", ErrNotFound
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return "", err
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(parentOID))
	if err != nil {
		return "", err
	}
	state := materialize.TaskRegistry.Fold(events)
	if state.Meta == nil {
		return "", ErrNotFound
	}
	status, _ := state.Meta["status"].(string)
	return status, nil
}
