// Package entitychain factors out the append-one-event-and-CAS-advance-ref
// loop shared by every feature's app service (issueapp, taskapp, chatapp,
// notifyapp): fold the entity's current events plus a new one, render the
// resulting state to a tree, commit it, and retry against concurrent local
// writers (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」,
// docs/14-testing.md シナリオ11).
//
// It was extracted once a third feature needed the identical pattern,
// per docs/13-roadmap.md's "2 例目まで複製、3 例目で抽出" policy.
package entitychain

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/sign"
)

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers.
var ErrRetriesExhausted = errors.New("entitychain: local ref update retries exhausted")

const defaultRetries = 10

// writeLocks serializes Append calls that target the same repository
// directory, across all Writer instances and features. This is purely an
// in-process safeguard: go-git's loose-object writer (temp file + rename)
// is not safe against concurrent goroutines writing into the same
// repository - on Windows this fails outright ("Access is denied" on the
// rename) rather than merely racing. Cross-process concurrency (e.g. two
// separate `githive` invocations, docs/03-sync-and-concurrency.md「クラッ
// シュ安全性とローカル競合」) is unaffected and continues to rely on git's
// atomic compare-and-swap ref update, since other processes have their own
// independent lock table.
var writeLocks sync.Map // map[string]*sync.Mutex

func lockFor(dir string) *sync.Mutex {
	key := dir
	if abs, err := filepath.Abs(dir); err == nil {
		key = abs
	}
	v, _ := writeLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Writer drives reads and CAS-safe writes for one feature's ref(s).
type Writer struct {
	Dir string
	// RefFor maps an entity id to its ref name. For singleton refs (e.g.
	// notify/stream) it should ignore id and always return the same name.
	RefFor func(id string) plumbing.ReferenceName
	// Registry folds this feature's events into a state.
	Registry *materialize.Registry
	// TreeFiles renders a fold state into the feature's tree layout.
	TreeFiles func(*materialize.State) (map[string][]byte, error)
	// NotFoundErr, if non-nil, is returned when a mutation targets an id
	// whose fold produces a nil Meta (i.e. no create event has ever been
	// accepted for it). Leave nil for features without a "does this
	// exist" concept (e.g. the notify stream).
	NotFoundErr error
}

// foldInputs returns what to fold id's current state from: a base state to
// start from (nil = start empty) and the events to apply on top, plus the
// ref's current OID.
//
// When the chain carries a usable checkpoint, the base is that checkpoint's
// snapshot and only the events after it are returned - the read shortcut of
// docs/03-sync-and-concurrency.md「チェックポイント」. Otherwise it degrades
// to the full walk, which produces the same state, just slower.
// newEventID, when non-empty, is the ID of an event the caller is about to
// append; it participates in the shortcut's ordering check just like the
// events already on the chain.
func (w *Writer) foldInputs(ctx context.Context, id, newEventID string) (base *materialize.State, events []*event.Envelope, oid string, err error) {
	r := gitx.New(w.Dir)
	ref := w.RefFor(id)
	oid, err = r.RevParse(ctx, ref.String())
	if err != nil {
		return nil, nil, "", err
	}
	if oid == "" {
		return nil, nil, gitx.ZeroOID, nil
	}
	repo, err := chain.OpenRepository(w.Dir)
	if err != nil {
		return nil, nil, "", err
	}
	head := plumbing.NewHash(oid)

	start, err := chain.FindCheckpointStart(repo, head)
	if err != nil {
		return nil, nil, "", err
	}
	if start != nil {
		if base, ok := w.checkpointBase(start, newEventID); ok {
			return base, start.After, oid, nil
		}
	}

	events, err = chain.WalkChain(repo, head)
	if err != nil {
		return nil, nil, "", err
	}
	return nil, events, oid, nil
}

// checkpointBase restores start's snapshot if it is safe to fold from
// (ADR-0016 条件 2・3). Any doubt - an older checkpoint format, a snapshot
// this tool cannot decode, or an event after the checkpoint that would sort
// before something the checkpoint already subsumed - answers "no", and the
// caller falls back to walking the whole chain.
func (w *Writer) checkpointBase(start *chain.CheckpointStart, newEventID string) (*materialize.State, bool) {
	maxID, ok := start.Checkpoint.Data["max_event_id"].(string)
	if !ok || !event.IsValidULID(maxID) {
		return nil, false
	}
	ids := make([]string, 0, len(start.After)+1)
	for _, e := range start.After {
		ids = append(ids, e.ID)
	}
	if newEventID != "" {
		ids = append(ids, newEventID)
	}
	for _, id := range ids {
		if id <= maxID {
			// A clock-skewed peer's event sorts inside the range the
			// checkpoint already folded; restarting here would apply events
			// in a different order than a full fold (ADR-0012).
			return nil, false
		}
	}
	raw, ok := start.Checkpoint.Data["state"].(map[string]any)
	if !ok {
		return nil, false
	}
	base, err := w.Registry.StateFromMap(raw)
	if err != nil {
		return nil, false
	}
	return base, true
}

// CurrentEvents returns every event in id's chain (empty if it does not
// exist yet) and the ref's current OID (gitx.ZeroOID if absent). It always
// walks the whole chain: callers that want the event list want all of it.
// State reconstruction goes through foldInputs instead.
func (w *Writer) CurrentEvents(ctx context.Context, id string) (events []*event.Envelope, oid string, err error) {
	r := gitx.New(w.Dir)
	ref := w.RefFor(id)
	oid, err = r.RevParse(ctx, ref.String())
	if err != nil {
		return nil, "", err
	}
	if oid == "" {
		return nil, gitx.ZeroOID, nil
	}
	repo, err := chain.OpenRepository(w.Dir)
	if err != nil {
		return nil, "", err
	}
	events, err = chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, "", err
	}
	return events, oid, nil
}

// Append folds buildEvent()'s event on top of id's current events, writes a
// new commit, and CAS-advances the ref, retrying on a lost race. It returns
// the resulting fold state so callers can detect no-op writes (e.g. an
// invalid status transition, which fold silently ignores).
func (w *Writer) Append(ctx context.Context, id string, buildEvent func() (*event.Envelope, string)) (*materialize.State, error) {
	mu := lockFor(w.Dir)
	mu.Lock()
	defer mu.Unlock()

	r := gitx.New(w.Dir)
	repo, err := chain.OpenRepository(w.Dir)
	if err != nil {
		return nil, err
	}
	sig, err := identity.Resolve(ctx, w.Dir)
	if err != nil {
		return nil, err
	}
	// Resolved once per Append rather than per attempt: the signing config
	// cannot change mid-loop, and a literal user.signingkey costs a temp
	// file (ADR-0014).
	signer, cleanupSigner, err := sign.ResolveSigner(ctx, w.Dir)
	if err != nil {
		return nil, err
	}
	defer cleanupSigner()
	ref := w.RefFor(id)

	for attempt := 0; attempt < defaultRetries; attempt++ {
		env, summary := buildEvent()
		env.Actor = sig.Email
		base, existingEvents, oid, err := w.foldInputs(ctx, id, env.ID)
		if err != nil {
			return nil, err
		}
		allEvents := append(append([]*event.Envelope(nil), existingEvents...), env)
		state := w.Registry.FoldFrom(base, allEvents)
		if w.NotFoundErr != nil && state.Meta == nil {
			return nil, w.NotFoundErr
		}

		files, err := w.TreeFiles(state)
		if err != nil {
			return nil, err
		}
		var parent plumbing.Hash
		if oid != gitx.ZeroOID {
			parent = plumbing.NewHash(oid)
		}
		newHash, err := chain.AppendEvent(repo, parent, env, summary, files, sig, chain.WithSigner(signer))
		if err != nil {
			return nil, err
		}
		if err := r.UpdateRef(ctx, ref.String(), newHash.String(), oid); err != nil {
			if errors.Is(err, gitx.ErrRefCASMismatch) {
				continue // someone else advanced the ref first; reread and retry
			}
			return nil, err
		}
		return state, nil
	}
	return nil, ErrRetriesExhausted
}

// Fold returns the current fold state for id without writing anything,
// taking the checkpoint shortcut when the chain offers one.
func (w *Writer) Fold(ctx context.Context, id string) (*materialize.State, error) {
	base, events, _, err := w.foldInputs(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return w.Registry.FoldFrom(base, events), nil
}
