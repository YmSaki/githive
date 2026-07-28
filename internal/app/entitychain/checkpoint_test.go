package entitychain

import (
	"context"
	"os/exec"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
)

const testRef = "refs/projects/issue/01j8x0a2b3c4d5e6f7g8h9j0ka"
const testEntity = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{
		{"user.email", "tester@example.com"},
		{"user.name", "Tester"},
		{"commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", kv[0], err, out)
		}
	}
	return dir
}

// testTreeFiles renders the whole fold state (including Scratch) into one
// file. Deliberately not a real feature's layout: this package's tests are
// about the fold inputs, and importing a feature app here would be an import
// cycle.
func testTreeFiles(s *materialize.State) (map[string][]byte, error) {
	m, err := materialize.StateToMap(s)
	if err != nil {
		return nil, err
	}
	b, err := event.Encode(m)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"state.json": b}, nil
}

func testWriter(dir string) *Writer {
	return &Writer{
		Dir:       dir,
		RefFor:    func(string) plumbing.ReferenceName { return plumbing.ReferenceName(testRef) },
		Registry:  materialize.IssueRegistry,
		TreeFiles: testTreeFiles,
	}
}

var testIDSeq = 0

func nextTestID(t *testing.T) string {
	t.Helper()
	testIDSeq++
	const base32 = "0123456789abcdefghjkmnpqrstvwxyz"
	if testIDSeq >= 32*32 {
		t.Fatal("ran out of test event ids")
	}
	return "01k3d0000000000000000000" + string(base32[(testIDSeq>>5)&31]) + string(base32[testIDSeq&31])
}

func appendEvent(t *testing.T, w *Writer, kind string, data map[string]any) string {
	t.Helper()
	var id string
	_, err := w.Append(context.Background(), testEntity, func() (*event.Envelope, string) {
		id = nextTestID(t)
		return &event.Envelope{
			V: event.SchemaVersion, Kind: kind, ID: id,
			TS: "2026-07-28T09:00:00.000Z", Entity: testEntity,
			Data: data, Extra: map[string]any{},
		}, kind
	})
	if err != nil {
		t.Fatalf("append %s: %v", kind, err)
	}
	return id
}

// appendCheckpoint writes a checkpoint the way fsckapp does, so these tests
// exercise the format actually produced in production.
func appendCheckpoint(t *testing.T, w *Writer, dir string, withScratch, withMaxID bool) {
	t.Helper()
	ctx := context.Background()
	events, oid, err := w.CurrentEvents(ctx, testEntity)
	if err != nil {
		t.Fatal(err)
	}
	stateMap, err := materialize.StateToMap(w.Registry.Fold(events))
	if err != nil {
		t.Fatal(err)
	}
	if !withScratch {
		delete(stateMap, "scratch")
	}
	maxID := ""
	for _, e := range events {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	data := map[string]any{"event_count": len(events), "head": oid, "state": stateMap}
	if withMaxID {
		data["max_event_id"] = maxID
	}
	appendEvent(t, w, "issue.checkpoint", data)
}

// fullFold is the reference answer: walk the entire chain and fold it from
// empty, exactly as githive did before the shortcut existed.
func fullFold(t *testing.T, w *Writer) *materialize.State {
	t.Helper()
	events, _, err := w.CurrentEvents(context.Background(), testEntity)
	if err != nil {
		t.Fatal(err)
	}
	return w.Registry.Fold(events)
}

func treeBytes(t *testing.T, s *materialize.State) string {
	t.Helper()
	files, err := testTreeFiles(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(files["state.json"])
}

func seedChain(t *testing.T, w *Writer) {
	t.Helper()
	appendEvent(t, w, "issue.create", map[string]any{"title": "seed", "body": "b"})
	appendEvent(t, w, "issue.label", map[string]any{"add": []any{"bug", "docs"}})
	appendEvent(t, w, "issue.assign", map[string]any{"add": []any{"a@example.com"}})
	appendEvent(t, w, "issue.comment", map[string]any{"body": "c1"})
}

// TestShortcutEngagesAndMatchesFullWalk is the end-to-end statement of A3:
// once a chain carries a checkpoint, reads restart from it, and the state
// they produce is byte-identical to walking the whole chain.
func TestShortcutEngagesAndMatchesFullWalk(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	w := testWriter(dir)

	seedChain(t, w)
	appendCheckpoint(t, w, dir, true, true)
	appendEvent(t, w, "issue.comment", map[string]any{"body": "after-1"})
	appendEvent(t, w, "issue.label", map[string]any{"remove": []any{"bug"}, "add": []any{"perf"}})

	base, events, _, err := w.foldInputs(context.Background(), testEntity, "")
	if err != nil {
		t.Fatal(err)
	}
	if base == nil {
		t.Fatal("expected the checkpoint shortcut to engage")
	}
	if len(events) != 2 {
		t.Errorf("expected only the 2 post-checkpoint events, got %d", len(events))
	}

	shortcut := w.Registry.FoldFrom(base, events)
	if got, want := treeBytes(t, shortcut), treeBytes(t, fullFold(t, w)); got != want {
		t.Errorf("shortcut state differs from full walk:\n%s\n---\n%s", got, want)
	}
}

// TestShortcutSkippedAfterMerge covers docs/03's linearity requirement: past
// a merge commit the checkpoint no longer subsumes everything behind head,
// so the shortcut must not engage.
func TestShortcutSkippedAfterMerge(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	w := testWriter(dir)
	ctx := context.Background()

	seedChain(t, w)
	appendCheckpoint(t, w, dir, true, true)
	appendEvent(t, w, "issue.comment", map[string]any{"body": "after-1"})

	// Turn the head into a merge commit over its own parent, the cheapest
	// way to produce the two-parent shape sync creates.
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, testRef)
	if err != nil {
		t.Fatal(err)
	}
	head := plumbing.NewHash(oid)
	headCommit, err := repo.CommitObject(head)
	if err != nil {
		t.Fatal(err)
	}
	files, err := testTreeFiles(fullFold(t, w))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	mergeHash, err := chain.AppendMerge(repo, [2]plumbing.Hash{head, headCommit.ParentHashes[0]}, files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dir, plumbing.ReferenceName(testRef), mergeHash, oid); err != nil {
		t.Fatal(err)
	}

	base, events, _, err := w.foldInputs(ctx, testEntity, "")
	if err != nil {
		t.Fatal(err)
	}
	if base != nil {
		t.Error("expected the shortcut to be skipped past a merge commit")
	}
	shortcut := w.Registry.FoldFrom(base, events)
	if got, want := treeBytes(t, shortcut), treeBytes(t, fullFold(t, w)); got != want {
		t.Errorf("fallback state differs from full walk:\n%s\n---\n%s", got, want)
	}
}

// TestShortcutSkippedForLegacyCheckpoints covers the two ways a checkpoint
// can be unusable: it predates the scratch key, or it predates max_event_id
// (ADR-0016 条件 2・3). Both must degrade to a full walk, not to a wrong
// answer.
func TestShortcutSkippedForLegacyCheckpoints(t *testing.T) {
	requireGit(t)
	for _, c := range []struct {
		name                  string
		withScratch, withMaxD bool
	}{
		{"no scratch key", false, true},
		{"no max_event_id", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := newTestRepo(t)
			w := testWriter(dir)
			seedChain(t, w)
			appendCheckpoint(t, w, dir, c.withScratch, c.withMaxD)
			appendEvent(t, w, "issue.comment", map[string]any{"body": "after"})

			base, events, _, err := w.foldInputs(context.Background(), testEntity, "")
			if err != nil {
				t.Fatal(err)
			}
			if base != nil {
				t.Error("expected the shortcut to be skipped for a legacy checkpoint")
			}
			shortcut := w.Registry.FoldFrom(base, events)
			if got, want := treeBytes(t, shortcut), treeBytes(t, fullFold(t, w)); got != want {
				t.Errorf("fallback state differs from full walk:\n%s\n---\n%s", got, want)
			}
		})
	}
}

// TestShortcutSkippedWhenEventSortsBeforeCheckpoint is the clock-skew guard
// (ADR-0016 条件 3): an event that lands after the checkpoint on the chain
// but before it in ULID order would be applied in a different position by
// the shortcut than by a full fold, so the shortcut must stand down.
func TestShortcutSkippedWhenEventSortsBeforeCheckpoint(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	w := testWriter(dir)
	ctx := context.Background()

	seedChain(t, w)
	appendCheckpoint(t, w, dir, true, true)

	// An event whose ULID predates everything already folded, as a peer with
	// a badly lagging clock would produce.
	_, err := w.Append(ctx, testEntity, func() (*event.Envelope, string) {
		return &event.Envelope{
			V: event.SchemaVersion, Kind: "issue.comment",
			ID: "01j00000000000000000000000", TS: "2026-01-01T00:00:00.000Z",
			Entity: testEntity, Data: map[string]any{"body": "from the past"},
			Extra: map[string]any{},
		}, "issue.comment"
	})
	if err != nil {
		t.Fatal(err)
	}

	base, events, _, err := w.foldInputs(ctx, testEntity, "")
	if err != nil {
		t.Fatal(err)
	}
	if base != nil {
		t.Error("expected the shortcut to be skipped when an event sorts before the checkpoint")
	}
	shortcut := w.Registry.FoldFrom(base, events)
	if got, want := treeBytes(t, shortcut), treeBytes(t, fullFold(t, w)); got != want {
		t.Errorf("fallback state differs from full walk:\n%s\n---\n%s", got, want)
	}
}
