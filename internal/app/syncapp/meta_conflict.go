package syncapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ErrMetaConfigConflict means equal-version configs disagree and require an
// explicit resolution. Repeating sync cannot decide which project is intended.
var ErrMetaConfigConflict = errors.New("syncapp: meta/config conflict")

// MetaConfigConflictError identifies both immutable config versions without
// discarding or advancing either side of their shared project history.
type MetaConfigConflictError struct{ LocalOID, RemoteOID string }

func (e *MetaConfigConflictError) Error() string {
	return fmt.Sprintf("%v: %s has different config.json values at the same schema_version (local %s, remote %s); inspect both configs and create a two-parent resolution commit before syncing again", ErrMetaConfigConflict, refspace.MetaConfigRef, e.LocalOID, e.RemoteOID)
}
func (e *MetaConfigConflictError) Unwrap() error { return ErrMetaConfigConflict }

// Recovery names a read-only inspection command and the required safe merge.
func (e *MetaConfigConflictError) Recovery() string {
	return fmt.Sprintf("git show %s:config.json; git show %s:config.json; resolve config.json with a two-parent commit preserving both histories (ADR-0025), then githive sync", e.LocalOID, e.RemoteOID)
}

// CheckMetaConfigCompatible reports whether a local and a remote
// meta/config tip could be synced without a manual decision: absent or
// equal tips, or one tip descending from the other, pass; diverged tips pass
// only when their same-version config.json is canonically identical (the
// same rule sync applies, ADR-0025). wp start uses it before writing.
func CheckMetaConfigCompatible(ctx context.Context, dir, localOID, remoteOID string) error {
	if localOID == "" || remoteOID == "" || localOID == remoteOID {
		return nil
	}
	r := gitx.New(dir)
	for _, pair := range [][2]string{{localOID, remoteOID}, {remoteOID, localOID}} {
		ok, err := r.IsAncestor(ctx, pair[0], pair[1])
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	_, err := metaMergeFiles(ctx, dir, localOID, remoteOID)
	return err
}
