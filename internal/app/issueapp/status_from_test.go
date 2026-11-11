package issueapp

import (
	"context"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestIssueStatusRecordsObservedParentState(t *testing.T) {
	svc := newTestRepo(t)
	ctx := context.Background()
	id, err := svc.NewIssue(ctx, "audit", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"in_progress", "archived", "closed", "open"} {
		if _, err := svc.Status(ctx, id, to); err != nil {
			t.Fatal(err)
		}
	}
	assertIssueStatusFromParent(t, svc, id)
}

func TestConcurrentIssueStatusAuditMatchesCASParent(t *testing.T) {
	svc := newTestRepo(t)
	dir := svc.Dir
	ctx := context.Background()
	id, err := svc.NewIssue(ctx, "concurrent audit", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			to := "closed"
			if i%2 == 0 {
				to = "open"
			}
			_, err := New(dir).Status(ctx, id, to)
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertIssueStatusFromParent(t, svc, id)
}

func assertIssueStatusFromParent(t *testing.T, svc *Service, id string) {
	t.Helper()
	repo, err := chain.OpenRepository(svc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	_, oid, err := svc.writer.CurrentEvents(context.Background(), id)
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
		if env == nil || env.Kind != "issue.status" {
			continue
		}
		if len(commit.ParentHashes) != 1 {
			t.Fatal("status must have one parent")
		}
		prior, err := chain.WalkChain(repo, commit.ParentHashes[0])
		if err != nil {
			t.Fatal(err)
		}
		want := materialize.IssueRegistry.Fold(prior).Meta["status"]
		if env.Data["from"] != want {
			t.Errorf("%s from=%v want parent status %v", env.ID, env.Data["from"], want)
		}
		if _, err := event.FromMap(env.ToMap()); err != nil {
			t.Fatal(err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("fixture wrote no status events")
	}
}
