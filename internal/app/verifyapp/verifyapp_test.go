package verifyapp

import (
	"context"
	"os/exec"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "tester@example.com"}, {"user.name", "Tester"}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return dir
}

// TestVerifyRefUnsignedCommitsAreFlagged is the common-case check: with no
// signing configured (as in most of this test suite), every commit lacks a
// valid signature, and VerifyRef must report that rather than silently
// passing (docs/11-security.md「SSH 署名」).
func TestVerifyRefUnsignedCommitsAreFlagged(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	svc := issueapp.New(dir)
	id, err := svc.NewIssue(ctx, "t", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	report, err := VerifyRef(ctx, dir, "refs/projects/issue/"+id, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.CommitCount != 1 {
		t.Fatalf("expected 1 commit, got %d", report.CommitCount)
	}
	if report.OK() {
		t.Fatal("expected unsigned commit to be flagged as an issue")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == "invalid_or_missing_signature" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid_or_missing_signature issue, got %+v", report.Issues)
	}
}

func TestVerifyRefMissingRefIsEmptyReport(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	report, err := VerifyRef(context.Background(), dir, "refs/projects/issue/01j8x0a2b3c4d5e6f7g8h9j0ka", "")
	if err != nil {
		t.Fatal(err)
	}
	if report.CommitCount != 0 || !report.OK() {
		t.Errorf("expected an empty, OK report for a nonexistent ref, got %+v", report)
	}
}

func TestVerifyAllCoversMultipleFeatures(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	issueSvc := issueapp.New(dir)
	if _, err := issueSvc.NewIssue(ctx, "t", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	reports, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 ref report, got %d: %+v", len(reports), reports)
	}
}

// appendNotifyShardEvent appends a notify.post event directly onto shardRef
// (a refs/projects/notify/<yyyy-mm> monthly shard, D8), bypassing
// notifyapp.Service, which stays hard-coded to the stream ref
// (notifyapp.go「シャード読み手の集約は移行時に実装」). It uses
// entitychain.Writer directly, the same primitive notifyapp.Service uses
// internally, to build a chain VerifyAll can be pointed at ahead of the
// reader-side migration.
func appendNotifyShardEvent(t *testing.T, dir, shardRef, title string) {
	t.Helper()
	w := &entitychain.Writer{
		Dir:       dir,
		RefFor:    func(string) plumbing.ReferenceName { return plumbing.ReferenceName(shardRef) },
		Registry:  materialize.NotifyRegistry,
		TreeFiles: notifyapp.TreeFiles,
	}
	_, err := w.Append(context.Background(), "", func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: event.SchemaVersion, Kind: "notify.post", ID: eid, TS: ts,
			Entity: eid,
			Data:   map[string]any{"targets": []any{"all"}, "title": title},
			Extra:  map[string]any{},
		}, "notify.post: " + title
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestVerifyAllCoversNotifyMonthlyShard is D8's verify goal: a hand-built
// refs/projects/notify/2026-07 chain must be picked up by VerifyAll exactly
// like notify/stream - refspace.Parse now resolves it to Feature notify,
// which is all VerifyAll's dispatch switch needs (it never special-cases
// singleton vs. shard refs).
func TestVerifyAllCoversNotifyMonthlyShard(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	shardRef := "refs/projects/notify/2026-07"

	appendNotifyShardEvent(t, dir, shardRef, "shard notice")

	reports, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var found *RefReport
	for i, r := range reports {
		if r.Ref == shardRef {
			found = &reports[i]
		}
	}
	if found == nil {
		t.Fatalf("expected VerifyAll to include the notify shard ref, got %+v", reports)
	}
	if found.CommitCount != 1 {
		t.Errorf("expected 1 commit on the shard chain, got %d", found.CommitCount)
	}
}
