package verifyapp

import (
	"context"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
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
