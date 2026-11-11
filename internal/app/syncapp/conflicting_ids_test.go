package syncapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// Plumbing deliberately permits constructing corrupt history for diagnostics;
// normal app writes must reject it before advancing an authoritative ref.
func appendIdentityFixture(t *testing.T, dir, ref string, env *event.Envelope) string {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent := mustRevParse(t, dir, ref)
	files, err := chain.ReadTree(repo, plumbing.NewHash(parent))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := identity.Resolve(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := chain.AppendEvent(repo, plumbing.NewHash(parent), env, "fixture", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), hash, parent); err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

func firstIdentityEvent(t *testing.T, dir, ref string) *event.Envelope {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(mustRevParse(t, dir, ref)))
	if err != nil || len(events) != 1 {
		t.Fatalf("seed: %v %v", events, err)
	}
	return events[0]
}

func TestConflictingEventIDRejectsLinearReadsWritesAndCheckpoints(t *testing.T) {
	requireGit(t)
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint=%t", checkpoint), func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			dir := cloneRepo(t, origin, "a@example.com")
			ref := seedIssues(t, dir, 1)[0]
			id := strings.TrimPrefix(ref, "refs/projects/issue/")
			original := firstIdentityEvent(t, dir, ref)
			conflict := *original
			conflict.Data = map[string]any{"title": "contradictory"}
			parent := appendIdentityFixture(t, dir, ref, &conflict)
			if checkpoint {
				// The snapshot is well-shaped and refers to the actual parent, but its
				// covered history already contains contradictory event identities.
				state, err := materialize.StateToMap(materialize.IssueRegistry.Fold([]*event.Envelope{original}))
				if err != nil {
					t.Fatal(err)
				}
				cpID, ts := idgen.NewWithTimestamp()
				appendIdentityFixture(t, dir, ref, &event.Envelope{V: event.SchemaVersion, Kind: "issue.checkpoint", ID: cpID, TS: ts, Actor: original.Actor, Entity: id, Data: map[string]any{"head": parent, "max_event_id": original.ID, "event_count": 2, "state": state}})
			}
			before := mustRevParse(t, dir, ref)
			if _, err := issueapp.New(dir).Show(ctx, id); !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("read selected a winner: %v", err)
			}
			if err := issueapp.New(dir).Label(ctx, id, []string{"new"}, nil); !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("write accepted corrupt history: %v", err)
			}
			if _, err := Sync(ctx, dir, "origin", []string{ref}, 0); !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("sync pushed corrupt history: %v", err)
			}
			if got := mustRevParse(t, dir, ref); got != before {
				t.Fatalf("failed operation moved ref: %s -> %s", before, got)
			}
			if got := mustRevParse(t, origin, ref); got != "" {
				t.Fatalf("corrupt history reached origin: %s", got)
			}
			report, err := fsckapp.New(dir).Run(ctx, fsckapp.Options{Compact: true, MaxEvents: 1})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range report.Findings {
				if finding.Code == "conflicting_event_id" {
					found = true
				}
			}
			if report.OK() || !found || len(report.Checkpoints) != 0 || mustRevParse(t, dir, ref) != before {
				t.Fatalf("fsck compact accepted collision: %+v", report)
			}
			verified, err := verifyapp.VerifyRef(ctx, dir, ref, "")
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, problem := range verified.Issues {
				if problem.Code == "conflicting_event_id" {
					found = true
				}
			}
			if verified.OK() || !found {
				t.Fatalf("verify missed collision: %+v", verified)
			}
			// A raw Git writer can publish corruption. First-fetch sync must reject
			// adoption just as outbound sync rejects delivery.
			pushed, err := gitx.New(dir).Push(ctx, "origin", ref+":"+ref)
			if err != nil || len(pushed) != 1 || !pushed[0].OK {
				t.Fatalf("raw push fixture: %v %v", pushed, err)
			}
			receiver := cloneRepo(t, origin, "receiver@example.com")
			if _, err := Sync(ctx, receiver, "origin", []string{ref}, 0); !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("incoming fast-forward accepted corruption: %v", err)
			}
			if got := mustRevParse(t, receiver, ref); got != "" {
				t.Fatalf("failed adoption installed ref: %s", got)
			}

		})
	}
}

func TestSyncRejectsConflictingEventIDsInBothDirections(t *testing.T) {
	requireGit(t)
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			a := cloneRepo(t, origin, "a@example.com")
			b := cloneRepo(t, origin, "b@example.com")
			ref := seedIssues(t, a, 1)[0]
			if _, err := Sync(ctx, a, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := Sync(ctx, b, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			seed := firstIdentityEvent(t, a, ref)
			eid, ts := idgen.NewWithTimestamp()
			for _, dir := range []string{a, b} {
				appendIdentityFixture(t, dir, ref, &event.Envelope{V: event.SchemaVersion, Kind: "issue.label", ID: eid, TS: ts, Actor: seed.Actor, Entity: seed.Entity, Data: map[string]any{"add": []any{dir}}})
			}
			pusher, receiver := a, b
			if reverse {
				pusher, receiver = b, a
			}
			if _, err := Sync(ctx, pusher, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			beforeLocal := mustRevParse(t, receiver, ref)
			beforeRemote := mustRevParse(t, origin, ref)
			if _, err := Sync(ctx, receiver, "origin", []string{ref}, 0); !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("merge selected a winner: %v", err)
			}
			if mustRevParse(t, receiver, ref) != beforeLocal || mustRevParse(t, origin, ref) != beforeRemote {
				t.Fatal("rejected collision advanced a ref")
			}
		})
	}
}

func TestAppendRejectsNewEventWithConflictingExistingID(t *testing.T) {
	requireGit(t)
	for _, checkpointID := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint-id=%t", checkpointID), func(t *testing.T) {
			ctx := context.Background()
			dir := cloneRepo(t, initBareOrigin(t), "a@example.com")
			ref := seedIssues(t, dir, 1)[0]
			original := firstIdentityEvent(t, dir, ref)
			if checkpointID {
				state, err := materialize.StateToMap(materialize.IssueRegistry.Fold([]*event.Envelope{original}))
				if err != nil {
					t.Fatal(err)
				}
				cpID, ts := idgen.NewWithTimestamp()
				cp := &event.Envelope{V: event.SchemaVersion, Kind: "issue.checkpoint", ID: cpID, TS: ts, Actor: original.Actor, Entity: original.Entity, Data: map[string]any{"head": mustRevParse(t, dir, ref), "max_event_id": original.ID, "event_count": 1, "state": state}}
				appendIdentityFixture(t, dir, ref, cp)
				// This ID sorts after max_event_id, so the checkpoint shortcut would
				// otherwise omit the only envelope with which the new event conflicts.
				original = cp
			}
			before := mustRevParse(t, dir, ref)
			w := &entitychain.Writer{Dir: dir, RefFor: func(string) plumbing.ReferenceName { return plumbing.ReferenceName(ref) }, Registry: materialize.IssueRegistry, TreeFiles: issueapp.TreeFiles}
			_, err := w.Append(ctx, original.Entity, func() (*event.Envelope, string) {
				copy := *original
				copy.Kind = "issue.edit"
				copy.Data = map[string]any{"title": "replacement"}
				return &copy, "collision"
			})
			if !errors.Is(err, event.ErrConflictingEventID) {
				t.Fatalf("new event replaced existing ID: %v", err)
			}
			if mustRevParse(t, dir, ref) != before {
				t.Fatal("rejected append moved ref")
			}
		})
	}
}

func TestIdenticalEventCopiesRemainReadableAndDeduplicated(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := cloneRepo(t, initBareOrigin(t), "a@example.com")
	ref := seedIssues(t, dir, 1)[0]
	original := firstIdentityEvent(t, dir, ref)
	appendIdentityFixture(t, dir, ref, original)
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	head := plumbing.NewHash(mustRevParse(t, dir, ref))
	for _, since := range []bool{false, true} {
		var events []*event.Envelope
		if since {
			events, err = chain.WalkChainSince(repo, head, "2000-01-01T00:00:00.000Z")
		} else {
			events, err = chain.WalkChain(repo, head)
		}
		if err != nil || len(events) != 1 {
			t.Fatalf("identical copies should be one event: %v %v", events, err)
		}
	}
	if _, err := issueapp.New(dir).Show(ctx, original.Entity); err != nil {
		t.Fatal(err)
	}
	// A fresh append still succeeds after storage-level duplication.
	if err := issueapp.New(dir).Label(ctx, original.Entity, []string{"next"}, nil); err != nil {
		t.Fatal(err)
	}
}
