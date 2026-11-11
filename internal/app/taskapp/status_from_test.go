package taskapp

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestTaskStatusRecordsObservedParentState(t *testing.T) {
	svc := newTestRepo(t)
	dir := svc.Dir
	ctx := context.Background()
	id, err := svc.NewTask(ctx, "audit", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"doing", "done", "review", "done"} {
		if _, err := svc.Status(ctx, id, to, "audit note"); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, oid, err := svc.writer.CurrentEvents(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := chain.WalkCommits(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, commit := range commits {
		env, err := chain.ExtractEnvelope(commit)
		if err != nil {
			t.Fatal(err)
		}
		if env == nil || env.Kind != "task.status" {
			continue
		}
		prior, err := chain.WalkChain(repo, commit.ParentHashes[0])
		if err != nil {
			t.Fatal(err)
		}
		want := materialize.TaskRegistry.Fold(prior).Meta["status"]
		if env.Data["from"] != want {
			t.Errorf("%s from=%v want parent status %v", env.ID, env.Data["from"], want)
		}
		if env.Data["note"] != "audit note" {
			t.Errorf("note lost: %v", env.Data)
		}
		checked++
	}
	if checked != 4 {
		t.Fatalf("checked statuses=%d", checked)
	}
}
