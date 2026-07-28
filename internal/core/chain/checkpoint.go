package chain

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/event"
)

// CheckpointStart describes a usable read shortcut: the checkpoint event a
// fold can restart from, and the events committed after it.
type CheckpointStart struct {
	// Checkpoint is the checkpoint envelope closest to head.
	Checkpoint *event.Envelope
	// After holds every event between Checkpoint (exclusive) and head
	// (inclusive), in head-to-checkpoint walk order. Fold sorts by ID, so
	// the slice order does not matter to callers.
	After []*event.Envelope
}

// FindCheckpointStart walks back from head looking for a checkpoint event
// reachable over single-parent commits only, and returns it together with
// the events committed after it. It returns (nil, nil) - "no shortcut" -
// when the walk reaches a merge commit or the chain root first.
//
// The linearity requirement is docs/03-sync-and-concurrency.md's
// 「線形履歴の読み出し高速化（自分より前のイベントがすべてチェーン上の祖先
// である場合の起点）にのみ使う」: past a merge, the checkpoint no longer
// subsumes everything behind head, so restarting from it would drop events.
//
// A returned start is necessary but not sufficient for the shortcut to be
// correct; the caller must also check the checkpoint's max_event_id against
// After's event IDs (ADR-0016 条件 3).
func FindCheckpointStart(repo *git.Repository, head plumbing.Hash) (*CheckpointStart, error) {
	var after []*event.Envelope
	for h := head; h != plumbing.ZeroHash; {
		commit, err := repo.CommitObject(h)
		if err != nil {
			return nil, fmt.Errorf("chain: read commit %s: %w", h, err)
		}
		if len(commit.ParentHashes) > 1 {
			return nil, nil // merge commit: history is no longer linear
		}
		env, err := ExtractEnvelope(commit)
		if err != nil {
			return nil, err
		}
		if env != nil {
			if strings.HasSuffix(env.Kind, ".checkpoint") {
				return &CheckpointStart{Checkpoint: env, After: after}, nil
			}
			after = append(after, env)
		}
		if len(commit.ParentHashes) == 0 {
			return nil, nil // reached the chain root without a checkpoint
		}
		h = commit.ParentHashes[0]
	}
	return nil, nil
}
