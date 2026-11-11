package merge

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

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

func issueEditEvent(entity string, seq int, title string) *event.Envelope {
	return &event.Envelope{
		V: 1, Kind: "issue.edit", ID: newULID(seq), TS: "2026-07-04T00:00:00.000Z",
		Actor: "a@example.com", Entity: entity,
		Data: map[string]any{"title": title}, Extra: map[string]any{},
	}
}

// genEventSet mixes set-like label adds with last-write-wins title edits, so
// that a merge which loses or reorders events changes the folded title, not
// just a commutative label set.
func genEventSet(rng *rand.Rand, entity string, n int) []*event.Envelope {
	events := []*event.Envelope{issueCreateEvent(entity, 0)}
	for i := 1; i <= n; i++ {
		seq := i*1000 + rng.Intn(999)
		if i%2 == 0 {
			events = append(events, issueEditEvent(entity, seq, fmt.Sprintf("title-%d", i)))
		} else {
			events = append(events, issueLabelEvent(entity, seq, fmt.Sprintf("l%d", i)))
		}
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

	s1 := mustFold(t, materialize.IssueRegistry, a, b)
	s2 := mustFold(t, materialize.IssueRegistry, b, a)

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

	left := mustFold(t, materialize.IssueRegistry, mustUnion(t, a, b), c)
	right := mustFold(t, materialize.IssueRegistry, a, mustUnion(t, b, c))

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

	once := mustFold(t, materialize.IssueRegistry, all)
	twice := mustFold(t, materialize.IssueRegistry, all, all)

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
		got := metaSignature(t, mustFold(t, materialize.IssueRegistry, a, b))
		if got != want {
			t.Fatalf("trial %d: split merge diverged from whole-set fold:\nwant: %s\ngot:  %s", trial, want, got)
		}
	}
}

// TestMergeConvergesFromSharedAncestor mirrors what two real diverged commit
// chains look like once walked back to their root (chain.WalkChain returns
// each side's full history, so a shared ancestor appears in both sets):
// side A is root+evA, side B is root+evB. UnionEvents/Fold must dedupe the
// shared root and converge to the same state regardless of merge order, with
// both branches' labels surviving - the property that used to be exercised
// via the git-backed Chains/TreeFiles helpers.
func TestMergeConvergesFromSharedAncestor(t *testing.T) {
	entity := "01j8x0a2b3c4d5e6f7g8h9j0ke"
	root := issueCreateEvent(entity, 0)
	evA := issueLabelEvent(entity, 1, "l1")
	evB := issueLabelEvent(entity, 2, "l2")

	sideA := []*event.Envelope{root, evA}
	sideB := []*event.Envelope{root, evB}

	stateAB := mustFold(t, materialize.IssueRegistry, sideA, sideB)
	stateBA := mustFold(t, materialize.IssueRegistry, sideB, sideA)

	gotAB, gotBA := metaSignature(t, stateAB), metaSignature(t, stateBA)
	if gotAB != gotBA {
		t.Errorf("merge tree differs by parent order:\nA,B: %s\nB,A: %s", gotAB, gotBA)
	}

	labels, _ := stateAB.Meta["labels"].([]any)
	if len(labels) != 2 {
		t.Errorf("expected both labels to survive the merge, got %v", labels)
	}
}

func mustFold(t *testing.T, registry *materialize.Registry, sets ...[]*event.Envelope) *materialize.State {
	t.Helper()
	state, err := Fold(registry, sets...)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func mustUnion(t *testing.T, sets ...[]*event.Envelope) []*event.Envelope {
	t.Helper()
	events, err := UnionEvents(sets...)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestMergeRejectsConflictingEventIdentitiesInBothDirections(t *testing.T) {
	a := issueLabelEvent("01j8x0a2b3c4d5e6f7g8h9j0ka", 1, "from-a")
	b := issueLabelEvent(a.Entity, 1, "from-b")
	for _, sets := range [][][]*event.Envelope{{{a}, {b}}, {{b}, {a}}, {{a, b}}, {{b, a}}} {
		if events, err := UnionEvents(sets...); !errors.Is(err, event.ErrConflictingEventID) || events != nil {
			t.Fatalf("union silently selected a winner: %v %v", events, err)
		}
		if state, err := Fold(materialize.IssueRegistry, sets...); !errors.Is(err, event.ErrConflictingEventID) || state != nil {
			t.Fatalf("fold silently selected a winner: %v %v", state, err)
		}
	}
	copies, err := UnionEvents([]*event.Envelope{a}, []*event.Envelope{a})
	if err != nil || len(copies) != 1 {
		t.Fatalf("identical copies: %v %v", copies, err)
	}
}
