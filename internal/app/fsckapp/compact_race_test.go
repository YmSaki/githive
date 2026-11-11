package fsckapp

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// TestCompactWithStaleSnapshotKeepsConcurrentWrite is issue #95's writer-side
// regression test. fsck enumerates refs once, so a write E can land between
// that enumeration and the checkpoint append. The checkpoint must then
// describe the chain it actually sits on (including E), and the read
// shortcut must not lose E.
func TestCompactWithStaleSnapshotKeepsConcurrentWrite(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	svc := chatapp.New(dir)
	id, err := svc.NewThread(ctx, "t", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Post(ctx, id, "second", "", ""); err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/chat/" + id
	r := gitx.New(dir)
	staleOID, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}

	// The concurrent write E, after fsck has already read staleOID.
	if err := svc.Post(ctx, id, "concurrent E", "", ""); err != nil {
		t.Fatal(err)
	}
	parentOID, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := refspace.Parse(ref)
	if err != nil {
		t.Fatal(err)
	}
	cp, _, err := New(dir).maybeCheckpoint(ctx, repo, ref, parsed, staleOID, Options{Compact: true, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil {
		t.Fatal("expected a checkpoint")
	}
	if cp.EventCount != 3 {
		t.Errorf("checkpoint event_count = %d, want 3 (including E)", cp.EventCount)
	}

	headOID, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.CommitObject(plumbing.NewHash(headOID))
	if err != nil {
		t.Fatal(err)
	}
	env, err := chain.ExtractEnvelope(head)
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Data["head"]; got != parentOID {
		t.Errorf("checkpoint data.head = %v, want its real parent %s", got, parentOID)
	}

	before := chatRefState(t, dir, id)
	shown, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown.Messages) != 3 {
		t.Errorf("shortcut read shows %d messages, want 3", len(shown.Messages))
	}

	// The next write folds through the shortcut too; its tree must still
	// carry E.
	if err := svc.Post(ctx, id, "after", "", ""); err != nil {
		t.Fatal(err)
	}
	if after := chatRefState(t, dir, id); after == before {
		t.Fatal("post after the checkpoint did not change state")
	}
	assertTreeMatchesFullWalk(t, dir, ref, materialize.ChatRegistry, chatapp.TreeFiles)
}
