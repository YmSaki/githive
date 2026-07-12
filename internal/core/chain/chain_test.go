package chain

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initBareDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func testEnvelope(id, entity, kind string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V:      1,
		Kind:   kind,
		ID:     id,
		TS:     "2026-07-04T12:00:00.000Z",
		Actor:  "tester@example.com",
		Entity: entity,
		Data:   data,
		Extra:  map[string]any{},
	}
}

func TestAppendEventAndWalkChain(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}

	entity := "01j8x0a2b3c4d5e6f7g8h9j0ka"
	env1 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t61", entity, "issue.create", map[string]any{"title": "first"})
	h1, err := AppendEvent(repo, ZeroHash, env1, "issue.create: first", map[string][]byte{
		"meta.json": []byte(`{"title":"first"}` + "\n"),
	}, sig)
	if err != nil {
		t.Fatal(err)
	}

	env2 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t62", entity, "issue.comment", map[string]any{"body": "hi"})
	h2, err := AppendEvent(repo, h1, env2, "issue.comment: hi", map[string][]byte{
		"meta.json":              []byte(`{"title":"first"}` + "\n"),
		"comments/c1.md":         []byte("hi\n"),
		"comments/nested/x.json": []byte(`{}` + "\n"),
	}, sig)
	if err != nil {
		t.Fatal(err)
	}

	refName := plumbing.ReferenceName("refs/projects/issue/" + entity)
	if err := AdvanceRef(dir, refName, h2, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	envelopes, err := WalkChain(repo, h2)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envelopes))
	}
	seen := map[string]bool{}
	for _, e := range envelopes {
		seen[e.ID] = true
	}
	if !seen[env1.ID] || !seen[env2.ID] {
		t.Errorf("missing envelopes: %+v", envelopes)
	}

	files, err := ReadTree(repo, h2)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["comments/c1.md"]) != "hi\n" {
		t.Errorf("unexpected comments/c1.md: %q", files["comments/c1.md"])
	}
	if string(files["comments/nested/x.json"]) != "{}\n" {
		t.Errorf("unexpected nested file: %q", files["comments/nested/x.json"])
	}
	if string(files["meta.json"]) != `{"title":"first"}`+"\n" {
		t.Errorf("unexpected meta.json: %q", files["meta.json"])
	}
}

func TestAdvanceRefCASRejectsStaleOld(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kb"
	env := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t63", entity, "task.create", map[string]any{"title": "t"})
	h1, err := AppendEvent(repo, ZeroHash, env, "task.create: t", map[string][]byte{"meta.json": []byte("{}\n")}, sig)
	if err != nil {
		t.Fatal(err)
	}
	refName := plumbing.ReferenceName("refs/projects/task/" + entity)
	if err := AdvanceRef(dir, refName, h1, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	env2 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t64", entity, "task.note", map[string]any{"body": "n"})
	h2, err := AppendEvent(repo, h1, env2, "task.note: n", map[string][]byte{"meta.json": []byte("{}\n")}, sig)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong old OID must be rejected.
	if err := AdvanceRef(dir, refName, h2, gitx.ZeroOID); err == nil {
		t.Error("expected CAS rejection with stale old OID")
	}
	// Correct old OID succeeds.
	if err := AdvanceRef(dir, refName, h2, h1.String()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTreeDeterministic(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"b.json": []byte("{}\n"),
		"a/z.md": []byte("z\n"),
		"a/y.md": []byte("y\n"),
	}
	h1, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("expected identical tree hash for identical inputs, got %s vs %s", h1, h2)
	}
}

func TestOpenRepositoryMissingDir(t *testing.T) {
	if _, err := OpenRepository(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error opening nonexistent repository")
	}
}

func testEnvelopeTS(id, entity, kind, ts string) *event.Envelope {
	return &event.Envelope{
		V:      1,
		Kind:   kind,
		ID:     id,
		TS:     ts,
		Actor:  "tester@example.com",
		Entity: entity,
		Data:   map[string]any{},
		Extra:  map[string]any{},
	}
}

func appendTestCommit(t testing.TB, repo *git.Repository, parent plumbing.Hash, sig Signature, id, entity, kind, ts string) plumbing.Hash {
	t.Helper()
	env := testEnvelopeTS(id, entity, kind, ts)
	h, err := AppendEvent(repo, parent, env, kind, map[string][]byte{"meta.json": []byte("{}\n")}, sig)
	if err != nil {
		t.Fatalf("AppendEvent(%s): %v", id, err)
	}
	return h
}

// TestWalkChainSinceEquivalence checks that for a plain single-parent chain
// (no merges), WalkChainSince(head, since) returns exactly the same envelope
// set (by ID) as filtering WalkChain(head)'s full result down to TS >= since,
// for cutoffs below the oldest event, at a middle event, and above the
// newest event.
func TestWalkChainSinceEquivalence(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kc"

	ids := []string{
		"01j8xq4d3nbz9k7w2m5e8h1t71",
		"01j8xq4d3nbz9k7w2m5e8h1t72",
		"01j8xq4d3nbz9k7w2m5e8h1t73",
		"01j8xq4d3nbz9k7w2m5e8h1t74",
		"01j8xq4d3nbz9k7w2m5e8h1t75",
	}
	tsValues := []string{
		"2026-01-01T00:00:00.000Z",
		"2026-02-01T00:00:00.000Z",
		"2026-03-01T00:00:00.000Z",
		"2026-04-01T00:00:00.000Z",
		"2026-05-01T00:00:00.000Z",
	}

	var head plumbing.Hash
	for i, id := range ids {
		head = appendTestCommit(t, repo, head, sig, id, entity, "issue.comment", tsValues[i])
	}

	cutoffs := []string{
		"2025-12-01T00:00:00.000Z", // below oldest -> everything
		"2026-03-01T00:00:00.000Z", // exactly a middle ts -> inclusive
		"2026-12-01T00:00:00.000Z", // above newest -> nothing
	}

	full, err := WalkChain(repo, head)
	if err != nil {
		t.Fatal(err)
	}

	for _, cutoff := range cutoffs {
		want := map[string]bool{}
		for _, e := range full {
			if e.TS >= cutoff {
				want[e.ID] = true
			}
		}

		got, err := WalkChainSince(repo, head, cutoff)
		if err != nil {
			t.Fatalf("WalkChainSince(cutoff=%s): %v", cutoff, err)
		}
		gotSet := map[string]bool{}
		for _, e := range got {
			if e.TS < cutoff {
				t.Errorf("cutoff=%s: WalkChainSince returned envelope %s with ts %s < cutoff", cutoff, e.ID, e.TS)
			}
			gotSet[e.ID] = true
		}

		if len(gotSet) != len(want) {
			t.Errorf("cutoff=%s: got %d envelopes, want %d (got=%v want=%v)", cutoff, len(gotSet), len(want), gotSet, want)
			continue
		}
		for id := range want {
			if !gotSet[id] {
				t.Errorf("cutoff=%s: missing expected envelope %s", cutoff, id)
			}
		}
	}
}

// TestWalkChainSinceMergePreservesNewerBranch guards against the exact bug
// the design deliberately avoids: pruning at or before a merge commit based
// on one parent branch's age. A merge commit combines two potentially
// unrelated timelines (docs/02-data-model.md "event-union マージ"), so an
// early-exit tied to one parent's age must never suppress a sibling parent
// branch that still has events at or after the cutoff.
func TestWalkChainSinceMergePreservesNewerBranch(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}

	cutoff := "2026-06-01T00:00:00.000Z"

	// Branch A: entirely older than cutoff.
	entityA := "01j8x0a2b3c4d5e6f7g8h9j0kd"
	oldA1 := appendTestCommit(t, repo, ZeroHash, sig, "01j8xq4d3nbz9k7w2m5e8h1t81", entityA, "issue.create", "2026-01-01T00:00:00.000Z")
	oldA2 := appendTestCommit(t, repo, oldA1, sig, "01j8xq4d3nbz9k7w2m5e8h1t82", entityA, "issue.comment", "2026-02-01T00:00:00.000Z")

	// Branch B: starts older than cutoff, ends newer than cutoff.
	entityB := "01j8x0a2b3c4d5e6f7g8h9j0ke"
	oldB1 := appendTestCommit(t, repo, ZeroHash, sig, "01j8xq4d3nbz9k7w2m5e8h1t83", entityB, "issue.create", "2026-03-01T00:00:00.000Z")
	newB2 := appendTestCommit(t, repo, oldB1, sig, "01j8xq4d3nbz9k7w2m5e8h1t84", entityB, "issue.comment", "2026-07-01T00:00:00.000Z")

	mergeFiles := map[string][]byte{"meta.json": []byte("{}\n")}
	mergeHead, err := AppendMerge(repo, [2]plumbing.Hash{oldA2, newB2}, mergeFiles, sig)
	if err != nil {
		t.Fatal(err)
	}

	got, err := WalkChainSince(repo, mergeHead, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	gotSet := map[string]bool{}
	for _, e := range got {
		gotSet[e.ID] = true
	}

	if !gotSet["01j8xq4d3nbz9k7w2m5e8h1t84"] {
		t.Errorf("branch B's newer-than-cutoff envelope was incorrectly dropped by merge-boundary pruning: got=%v", gotSet)
	}
	if gotSet["01j8xq4d3nbz9k7w2m5e8h1t81"] || gotSet["01j8xq4d3nbz9k7w2m5e8h1t82"] || gotSet["01j8xq4d3nbz9k7w2m5e8h1t83"] {
		t.Errorf("older-than-cutoff envelopes should have been pruned: got=%v", gotSet)
	}
}

// idAtSeq renders a 26-char lowercase-ULID-shaped id that encodes seq in its
// tail, avoiding the excluded Crockford Base32 letters (i, l, o, u), for
// synthetic long chains where hand-writing one literal per commit would be
// impractical.
func idAtSeq(seq int) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz" // 32 chars, ulidRe-safe
	b := []byte("01j8xq4d3nbz9k7w2m5e8h0000")
	for i := 0; i < 4; i++ {
		b[len(b)-1-i] = alphabet[seq%len(alphabet)]
		seq /= len(alphabet)
	}
	return string(b)
}

// BenchmarkWalkChainSinceVsWalkChain demonstrates that, for a long
// single-parent chain, WalkChainSince with a cutoff near the newest end
// visits far fewer commits than a full WalkChain (which must decode every
// commit back to the root before the caller's own filter discards them).
func BenchmarkWalkChainSinceVsWalkChain(b *testing.B) {
	dir := b.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git init: %v\n%s", err, out)
	}
	repo, err := OpenRepository(dir)
	if err != nil {
		b.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kf"

	const n = 500
	var head plumbing.Hash
	for i := 0; i < n; i++ {
		ts := fmt.Sprintf("2020-01-%02dT00:00:00.000Z", (i%28)+1)
		if i >= n-5 {
			ts = "2026-07-01T00:00:00.000Z" // last few commits are recent
		}
		head = appendTestCommit(b, repo, head, sig, idAtSeq(i), entity, "issue.comment", ts)
	}

	cutoff := "2026-01-01T00:00:00.000Z" // only the last few commits qualify

	b.Run("WalkChain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := WalkChain(repo, head); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("WalkChainSince", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := WalkChainSince(repo, head, cutoff); err != nil {
				b.Fatal(err)
			}
		}
	})
}
