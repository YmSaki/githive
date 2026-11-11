package fsckapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
)

func TestOversizedCheckpointLeavesOriginalHistoryReadable(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := issueapp.New(dir)
	id, err := svc.NewIssue(ctx, "large-state", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := svc.Comment(ctx, id, strings.Repeat("x", event.MaxEventBytes/2), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	ref := "refs/projects/issue/" + id
	r := gitx.New(dir)
	before, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2}); !errors.Is(err, event.ErrEventTooLarge) {
		t.Fatalf("expected oversized checkpoint refusal: %v", err)
	}
	after, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("failed compaction advanced ref")
	}
	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Comments) != 2 {
		t.Fatalf("comments lost: %d", len(show.Comments))
	}
}

func TestFsckReportsForeignOversizedEvent(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := issueapp.New(dir)
	id, err := svc.NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.CommitObject(plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	env, err := chain.ExtractEnvelope(head)
	if err != nil {
		t.Fatal(err)
	}
	env.Data["body"] = strings.Repeat("x", event.MaxEventBytes)
	raw, err := event.Encode(env.ToMap())
	if err != nil {
		t.Fatal(err)
	}
	foreign := *head
	foreign.Message = "foreign oversized event\n\n" + string(raw)
	foreign.ParentHashes = []plumbing.Hash{head.Hash}
	encoded := repo.Storer.NewEncodedObject()
	if err := foreign.Encode(encoded); err != nil {
		t.Fatal(err)
	}
	hash, err := repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, ref, hash.String(), oid); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range report.Findings {
		if finding.Code == "event_too_large" && finding.Commit == hash.String() {
			return
		}
	}
	t.Fatalf("oversized foreign event not reported: %+v", report.Findings)
}
