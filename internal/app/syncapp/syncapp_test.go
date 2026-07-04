package syncapp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initBareOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", "--bare", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return dir
}

func cloneRepo(t *testing.T, origin, email string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "--quiet", origin, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", email}, {"user.name", email}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return dir
}

// TestSyncConvergesConcurrentUpdates mirrors docs/14-testing.md 標準シナリオ1
// 「同時更新の収束」: clone A and B each write a different event to the same
// issue, A pushes first, B's naive push is rejected, B syncs (merge+push),
// and A's next sync converges to the same final state.
func TestSyncConvergesConcurrentUpdates(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// A creates the issue and pushes it so B can see it.
	svcA := issueapp.New(dirA)
	id, err := svcA.NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	if _, err := Sync(ctx, dirA, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("A initial sync: %v", err)
	}

	// B fetches it in.
	if _, err := Sync(ctx, dirB, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("B initial sync: %v", err)
	}

	// A and B each add a different label, diverging.
	svcB := issueapp.New(dirB)
	if err := svcA.Label(ctx, id, []string{"from-a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svcB.Label(ctx, id, []string{"from-b"}, nil); err != nil {
		t.Fatal(err)
	}

	// A pushes first (fast-forward).
	resA, err := Sync(ctx, dirA, "origin", []string{ref}, 5)
	if err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if resA[0].Action != ActionFastForwardOut {
		t.Errorf("expected A's push to be a fast-forward, got %s", resA[0].Action)
	}

	// B's sync must detect divergence, merge, and push.
	resB, err := Sync(ctx, dirB, "origin", []string{ref}, 5)
	if err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if resB[0].Action != ActionMerged {
		t.Errorf("expected B's sync to merge, got %s", resB[0].Action)
	}

	// A syncs again and must converge to the same state B has.
	if _, err := Sync(ctx, dirA, "origin", []string{ref}, 5); err != nil {
		t.Fatalf("A converge sync: %v", err)
	}

	showA, err := svcA.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	showB, err := svcB.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	labelsA, _ := showA.Meta["labels"].([]any)
	labelsB, _ := showB.Meta["labels"].([]any)
	if len(labelsA) != 2 || len(labelsB) != 2 {
		t.Fatalf("expected both labels to survive convergence: A=%v B=%v", labelsA, labelsB)
	}
	if fmtLabels(labelsA) != fmtLabels(labelsB) {
		t.Errorf("A and B did not converge: A=%v B=%v", labelsA, labelsB)
	}
}

func fmtLabels(labels []any) string {
	s := ""
	for _, l := range labels {
		s += l.(string) + ","
	}
	return s
}

func TestSyncUpToDate(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()

	svcA := issueapp.New(dirA)
	id, err := svcA.NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	if _, err := Sync(ctx, dirA, "origin", []string{ref}, 5); err != nil {
		t.Fatal(err)
	}
	results, err := Sync(ctx, dirA, "origin", []string{ref}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Action != ActionUpToDate {
		t.Errorf("expected up-to-date on second sync, got %s", results[0].Action)
	}
}
