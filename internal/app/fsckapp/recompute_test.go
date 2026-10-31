package fsckapp

import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
)

func findingsWithCode(report *Report, code string) []Finding {
	var out []Finding
	for _, f := range report.Findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

// TestTamperedTreeReported is docs/14 シナリオ 6 / #115: a commit with the
// same message and parent as head but a hand-edited tree must be reported
// as tree_mismatch.
func TestTamperedTreeReported(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	id, err := issueapp.New(dir).NewIssue(ctx, "original", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	head, err := object.GetCommit(repo.Storer, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	files, err := chain.ReadTree(repo, head.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["meta.json"]; !ok {
		t.Fatalf("expected meta.json in the issue tree, got %v", files)
	}
	files["meta.json"] = []byte(`{"title": "TAMPERED"}` + "\n")
	tree, err := chain.WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	forged := &object.Commit{
		Author: head.Author, Committer: head.Committer, Message: head.Message,
		TreeHash: tree, ParentHashes: head.ParentHashes,
	}
	enc := repo.Storer.NewEncodedObject()
	if err := forged.Encode(enc); err != nil {
		t.Fatal(err)
	}
	forgedHash, err := repo.Storer.SetEncodedObject(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, ref, forgedHash.String(), oid); err != nil {
		t.Fatal(err)
	}

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsWithCode(report, "tree_mismatch")
	if report.OK() || len(got) != 1 || got[0].Path != "meta.json" || got[0].Ref != ref {
		t.Fatalf("want one tree_mismatch on meta.json, got %+v", report.Findings)
	}
}

// TestForgedCheckpointStateReported: a checkpoint whose data.state does not
// match the fold of the events before it is reported, even though the
// head tree itself is fine (fold ignores checkpoints).
func TestForgedCheckpointStateReported(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := chatapp.New(dir)
	id, err := svc.NewThread(ctx, "t", "first")
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/chat/" + id

	// A legitimate checkpoint first: must not be reported.
	if _, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 1}); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("a genuine checkpoint must not be reported: %+v", report.Findings)
	}

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	files, err := chain.ReadTree(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	eid, ts := idgen.NewWithTimestamp()
	forged := &event.Envelope{
		V: event.SchemaVersion, Kind: "chat.checkpoint", ID: eid, TS: ts, Actor: "tester@example.com", Entity: id,
		Data: map[string]any{
			"event_count": 1, "head": oid, "max_event_id": eid,
			"state": map[string]any{"meta": map[string]any{"title": "forged"}, "collections": map[string]any{}, "scratch": map[string]any{}},
		},
		Extra: map[string]any{},
	}
	sig := object.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	newHead, err := chain.AppendEvent(repo, plumbing.NewHash(oid), forged, "chat.checkpoint", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, ref, newHead.String(), oid); err != nil {
		t.Fatal(err)
	}

	report, err = New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsWithCode(report, "checkpoint_state_mismatch")
	if len(got) != 1 || got[0].Commit != newHead.String() {
		t.Fatalf("want one checkpoint_state_mismatch on %s, got %+v", newHead, report.Findings)
	}
	if n := len(findingsWithCode(report, "tree_mismatch")); n != 0 {
		t.Fatalf("head tree is still correct, got %+v", report.Findings)
	}
}
