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

// CurrentEvents returns every event in id's chain (empty if it does not
// exist yet) and the ref's current OID (gitx.ZeroOID if absent).
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
	ref := w.RefFor(id)

	for attempt := 0; attempt < defaultRetries; attempt++ {
		existingEvents, oid, err := w.CurrentEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		env, summary := buildEvent()
		env.Actor = sig.Email
		allEvents := append(append([]*event.Envelope(nil), existingEvents...), env)
		state := w.Registry.Fold(allEvents)
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
		newHash, err := chain.AppendEvent(repo, parent, env, summary, files, sig)
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

// Fold returns the current fold state for id without writing anything.
func (w *Writer) Fold(ctx context.Context, id string) (*materialize.State, error) {
	events, _, err := w.CurrentEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	return w.Registry.Fold(events), nil
}
