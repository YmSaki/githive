package syncapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
)

// TestFsckCleanAfterEventUnionMerge guards fsck's recomputation check
// (#115) against false positives: the tree an event-union merge writes
// must be exactly the recomputed materialization.
func TestFsckCleanAfterEventUnionMerge(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")

	if err := usersapp.New(dirA).AddUser(ctx, "alice", "", "a@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	id, err := chatapp.New(dirA).NewThread(ctx, "t", "first")
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/chat/" + id
	if _, err := Sync(ctx, dirA, "origin", []string{ref, registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirB, "origin", []string{ref, registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	if err := chatapp.New(dirA).Post(ctx, id, "from a", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := chatapp.New(dirB).Post(ctx, id, "from b", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := usersapp.New(dirA).AddUser(ctx, "carol", "", "c@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := usersapp.New(dirB).AddUser(ctx, "dave", "", "d@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{ref, registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	results, err := Sync(ctx, dirB, "origin", []string{ref, registryRef}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Action != ActionMerged {
			t.Fatalf("fixture should have produced merges, got %+v", results)
		}
	}

	report, err := fsckapp.New(dirB).Run(ctx, fsckapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("fsck after merge: %+v", report.Findings)
	}
}
