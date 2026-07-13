package fsckapp

import (
	"context"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// featureWriter bundles the fold registry and tree renderer fsck needs to
// append a checkpoint to one feature's chain. The renderers are each feature
// app's own TreeFiles: the checkpoint commit's tree MUST be the feature's
// canonical materialized layout (front matter, jsonl, etc.), not the generic
// core/merge layout, or the head tree after a checkpoint would differ from
// what a feature write produces and break byte-identical materialization
// (docs/02-data-model.md「決定性の不変条件」). Because fold ignores checkpoint
// kinds, TreeFiles(fold(events + checkpoint)) == TreeFiles(fold(events)), so
// the tree is byte-identical to the pre-checkpoint head.
type featureWriter struct {
	registry  *materialize.Registry
	treeFiles func(*materialize.State) (map[string][]byte, error)
	// perEntity is true when the chain has a stable entity id shared by all
	// its events (issue/task/chat: the ref's ULID). Singleton streams
	// (notify/users) have no chain-level entity id, so the checkpoint reuses
	// its own event id as its entity, matching notifyapp's existing
	// Entity == event id convention (see docs/adr/0013).
	perEntity bool
}

var featureWriters = map[refspace.Feature]featureWriter{
	refspace.FeatureIssue:  {materialize.IssueRegistry, issueapp.TreeFiles, true},
	refspace.FeatureTask:   {materialize.TaskRegistry, taskapp.TreeFiles, true},
	refspace.FeatureChat:   {materialize.ChatRegistry, chatapp.TreeFiles, true},
	refspace.FeatureNotify: {materialize.NotifyRegistry, notifyapp.TreeFiles, false},
	refspace.FeatureUsers:  {materialize.UsersRegistry, usersapp.TreeFiles, false},
}

// maybeCheckpoint appends a checkpoint to ref's chain if it is past the
// threshold. A chain whose envelopes cannot be walked (corruption already
// reported by validateChain) is skipped rather than aborting the whole fsck.
func (s *Service) maybeCheckpoint(ctx context.Context, repo *git.Repository, ref string, parsed refspace.ParsedRef, oid string, opts Options) (*CheckpointInfo, []Finding, error) {
	fw, ok := featureWriters[parsed.Feature]
	if !ok {
		return nil, nil, nil
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, nil, nil
	}
	reason, due := shouldCompact(events, opts.maxEvents(), opts.maxAge(), opts.now())
	if !due {
		return nil, nil, nil
	}

	// Fold the snapshot to embed the current state in the checkpoint's data
	// (docs/03-sync-and-concurrency.md「data に … 完全な状態 …」). The commit's
	// tree is rendered independently by the Writer from the freshly re-read
	// chain, so this snapshot is only a read-hint and its exactness is not a
	// correctness requirement.
	state := fw.registry.Fold(events)
	eventCount := countNonCheckpoint(events)

	w := &entitychain.Writer{
		Dir:       s.Dir,
		RefFor:    func(string) plumbing.ReferenceName { return plumbing.ReferenceName(ref) },
		Registry:  fw.registry,
		TreeFiles: fw.treeFiles,
	}

	var cpEventID, cpEntity string
	if _, err := w.Append(ctx, parsed.ID, func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		cpEventID = eid
		if fw.perEntity {
			cpEntity = parsed.ID
		} else {
			cpEntity = eid
		}
		kind := string(parsed.Feature) + ".checkpoint"
		return &event.Envelope{
			V:      1,
			Kind:   kind,
			ID:     eid,
			TS:     ts,
			Entity: cpEntity,
			Data:   checkpointData(state, eventCount, oid),
			Extra:  map[string]any{},
		}, kind
	}); err != nil {
		return nil, nil, err
	}

	return &CheckpointInfo{
		Ref:        ref,
		Feature:    string(parsed.Feature),
		Entity:     cpEntity,
		EventID:    cpEventID,
		EventCount: eventCount,
		Reason:     reason,
	}, nil, nil
}

// checkpointData is the checkpoint event's data payload: the folded state at
// this point plus the number of events it subsumes and the head commit it sits
// on (docs/03-sync-and-concurrency.md「チェックポイント」).
func checkpointData(state *materialize.State, eventCount int, head string) map[string]any {
	return map[string]any{
		"event_count": eventCount,
		"head":        head,
		"state":       stateToMap(state),
	}
}

func stateToMap(state *materialize.State) map[string]any {
	meta := map[string]any{}
	if state.Meta != nil {
		meta = map[string]any(state.Meta)
	}
	collections := make(map[string]any, len(state.Collections))
	for name, coll := range state.Collections {
		collections[name] = map[string]any(coll)
	}
	return map[string]any{"meta": meta, "collections": collections}
}

func isCheckpoint(kind string) bool { return strings.HasSuffix(kind, ".checkpoint") }

func countNonCheckpoint(events []*event.Envelope) int {
	n := 0
	for _, e := range events {
		if !isCheckpoint(e.Kind) {
			n++
		}
	}
	return n
}

// shouldCompact decides whether a chain is past the checkpoint threshold
// (docs/03-sync-and-concurrency.md「チェックポイント」: 直前のチェックポイント
// から maxEvents イベント、または maxAge 経過). It returns the triggering
// reason ("event_count" or "age") and whether a checkpoint is due. A chain
// with no new events since its last checkpoint is never due (so repeated
// `fsck --compact` runs do not grow the chain without bound).
func shouldCompact(events []*event.Envelope, maxEvents int, maxAge time.Duration, now time.Time) (string, bool) {
	ordered := event.SortEvents(events)

	var lastCP *event.Envelope
	for _, e := range ordered {
		if isCheckpoint(e.Kind) {
			lastCP = e
		}
	}

	var sinceCount int
	var baselineTS string
	if lastCP != nil {
		baselineTS = lastCP.TS
		for _, e := range ordered {
			if !isCheckpoint(e.Kind) && e.ID > lastCP.ID {
				sinceCount++
			}
		}
	} else {
		sinceCount = countNonCheckpoint(ordered)
		if len(ordered) > 0 {
			baselineTS = ordered[0].TS
		}
	}

	if sinceCount == 0 {
		return "", false
	}
	if sinceCount >= maxEvents {
		return "event_count", true
	}
	if baselineTS != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", baselineTS); err == nil {
			if now.Sub(t) >= maxAge {
				return "age", true
			}
		}
	}
	return "", false
}
