package merge

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/oklog/ulid/v2"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newULID(seq int) string {
	entropy := ulid.Monotonic(rand.New(rand.NewSource(int64(seq))), 0)
	id := ulid.MustNew(ulid.Timestamp(time.Date(2026, 7, 4, 0, 0, 0, seq*1_000_000, time.UTC)), entropy).String()
	// docs/02-data-model.md「ID：ULID」requires lowercase Crockford Base32;
	// the oklog/ulid library renders uppercase.
	return strings.ToLower(id)
}

func issueCreateEvent(entity string, seq int) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: "issue.create", ID: newULID(seq), TS: "2026-07-04T00:00:00.000Z",
		Actor: "a@example.com", Entity: entity,
		Data: map[string]any{"title": "seed"}, Extra: map[string]any{},
	}
}

func issueLabelEvent(entity string, seq int, add string) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: "issue.label", ID: newULID(seq), TS: "2026-07-04T00:00:00.000Z",
		Actor: "a@example.com", Entity: entity,
		Data: map[string]any{"add": []any{add}}, Extra: map[string]any{},
	}
}

func genEventSet(rng *rand.Rand, entity string, n int) []*event.Envelope {
	events := []*event.Envelope{issueCreateEvent(entity, 0)}
	for i := 1; i <= n; i++ {
		events = append(events, issueLabelEvent(entity, i*1000+rng.Intn(999), fmt.Sprintf("l%d", i)))
	}
	return events
}

func metaSignature(t *testing.T, state *materialize.State) string {
	t.Helper()
	s, err := event.EncodeString(map[string]any(state.Meta))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestMergeCommutative: merging A,B gives the same result as merging B,A.
func TestMergeCommutative(t *testing.T) {
	entity := "01j8x0a2b3c4d5e6f7g8h9j0ka"
	rng := rand.New(rand.NewSource(1))
	all := genEventSet(rng, entity, 6)
	a := all[:3]
	b := all[3:]

	s1 := Fold(materialize.IssueRegistry, a, b)
	s2 := Fold(materialize.IssueRegistry, b, a)

	got1, got2 := metaSignature(t, s1), metaSignature(t, s2)
	if got1 != got2 {
		t.Errorf("merge not commutative:\nA,B: %s\nB,A: %s", got1, got2)
	}
}

// TestMergeAssociative: merging (A,B),C equals A,(B,C) for a 3-way split.
func TestMergeAssociative(t *testing.T) {
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kb"
	rng := rand.New(rand.NewSource(2))
	all := genEventSet(rng, entity, 9)
	a, b, c := all[0:3], all[3:6], all[6:9]

	left := Fold(materialize.IssueRegistry, UnionEvents(a, b), c)
	right := Fold(materialize.IssueRegistry, a, UnionEvents(b, c))

	gotLeft, gotRight := metaSignature(t, left), metaSignature(t, right)
	if gotLeft != gotRight {
		t.Errorf("merge not associative:\n(A,B),C: %s\nA,(B,C): %s", gotLeft, gotRight)
	}
}

// TestMergeIdempotent: merging a set with itself changes nothing.
func TestMergeIdempotent(t *testing.T) {
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kc"
	rng := rand.New(rand.NewSource(3))
	all := genEventSet(rng, entity, 5)

	once := Fold(materialize.IssueRegistry, all)
	twice := Fold(materialize.IssueRegistry, all, all)

	gotOnce, gotTwice := metaSignature(t, once), metaSignature(t, twice)
	if gotOnce != gotTwice {
		t.Errorf("merge not idempotent:\nonce:  %s\ntwice: %s", gotOnce, gotTwice)
	}
}

// TestMergeConvergesAcrossSplits verifies convergence under many random
// splits of the same event set into two, matching docs/14-testing.md
// 「マージの収束」 more broadly than the fixed-split tests above.
func TestMergeConvergesAcrossSplits(t *testing.T) {
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kd"
	rng := rand.New(rand.NewSource(4))
	all := genEventSet(rng, entity, 12)

	want := metaSignature(t, materialize.IssueRegistry.Fold(all))

	for trial := 0; trial < 20; trial++ {
		shuffled := append([]*event.Envelope(nil), all...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		splitAt := 1 + rng.Intn(len(shuffled)-1)
		a, b := shuffled[:splitAt], shuffled[splitAt:]
		got := metaSignature(t, Fold(materialize.IssueRegistry, a, b))
		if got != want {
			t.Fatalf("trial %d: split merge diverged from whole-set fold:\nwant: %s\ngot:  %s", trial, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// git-integration test: two real diverged chains, merged via Chains, produce
// identical tree content regardless of which side is "ours".
// ---------------------------------------------------------------------------

func initBareDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestChainsMergeConverges(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := chain.Signature{Name: "Tester", Email: "a@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0ke"

	root := issueCreateEvent(entity, 0)
	rootFiles, err := TreeFiles(materialize.IssueRegistry.Fold([]*event.Envelope{root}))
	if err != nil {
		t.Fatal(err)
	}
	rootHash, err := chain.AppendEvent(repo, chain.ZeroHash, root, "issue.create: seed", rootFiles, sig)
	if err != nil {
		t.Fatal(err)
	}

	// Side A adds label "l1", side B adds label "l2", both branching from root.
	evA := issueLabelEvent(entity, 1, "l1")
	filesA, err := TreeFiles(materialize.IssueRegistry.Fold([]*event.Envelope{root, evA}))
	if err != nil {
		t.Fatal(err)
	}
	headA, err := chain.AppendEvent(repo, rootHash, evA, "issue.label: +l1", filesA, sig)
	if err != nil {
		t.Fatal(err)
	}

	evB := issueLabelEvent(entity, 2, "l2")
	filesB, err := TreeFiles(materialize.IssueRegistry.Fold([]*event.Envelope{root, evB}))
	if err != nil {
		t.Fatal(err)
	}
	headB, err := chain.AppendEvent(repo, rootHash, evB, "issue.label: +l2", filesB, sig)
	if err != nil {
		t.Fatal(err)
	}

	mergeAB, err := Chains(repo, materialize.IssueRegistry, [2]plumbing.Hash{headA, headB}, sig)
	if err != nil {
		t.Fatal(err)
	}
	mergeBA, err := Chains(repo, materialize.IssueRegistry, [2]plumbing.Hash{headB, headA}, sig)
	if err != nil {
		t.Fatal(err)
	}

	commitAB, err := object.GetCommit(repo.Storer, mergeAB)
	if err != nil {
		t.Fatal(err)
	}
	commitBA, err := object.GetCommit(repo.Storer, mergeBA)
	if err != nil {
		t.Fatal(err)
	}
	if commitAB.TreeHash != commitBA.TreeHash {
		t.Errorf("merge tree differs by parent order: AB=%s BA=%s", commitAB.TreeHash, commitBA.TreeHash)
	}
	if commitAB.Message != "merge: event-union" {
		t.Errorf("unexpected merge commit message: %q", commitAB.Message)
	}

	filesMerged, err := chain.ReadTree(repo, mergeAB)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(filesMerged["meta.json"], &meta); err != nil {
		t.Fatal(err)
	}
	labels, _ := meta["labels"].([]any)
	if len(labels) != 2 {
		t.Errorf("expected both labels to survive the merge, got %v", labels)
	}
}
