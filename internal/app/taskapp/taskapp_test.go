package taskapp

import (
	"context"
	"os/exec"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) *Service {
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
	return New(dir)
}

func TestTaskLifecycle(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewTask(ctx, "sync のパス正規化を修正する", "詳細...", "", "2026-07-10", "high")
	if err != nil {
		t.Fatal(err)
	}

	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["owner"] != "tester@example.com" {
		t.Errorf("expected owner to default to actor, got %v", show.Meta["owner"])
	}
	if show.Meta["status"] != "todo" {
		t.Errorf("expected initial status todo, got %v", show.Meta["status"])
	}
	if show.Body != "詳細..." {
		t.Errorf("unexpected body: %q", show.Body)
	}

	ok, err := svc.Status(ctx, id, "doing", "着手します")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected valid transition todo->doing to succeed")
	}

	show, err = svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["status"] != "doing" {
		t.Errorf("expected doing, got %v", show.Meta["status"])
	}
	history, _ := show.Meta["status_history"].([]any)
	if len(history) != 2 {
		t.Errorf("expected 2 status_history entries, got %v", history)
	}
	if len(show.Notes) != 1 || show.Notes[0]["body"] != "着手します" {
		t.Errorf("expected the status note to appear in notes, got %+v", show.Notes)
	}

	if err := svc.Reassign(ctx, id, "other@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Note(ctx, id, "追加メモ"); err != nil {
		t.Fatal(err)
	}

	show, err = svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["owner"] != "other@example.com" {
		t.Errorf("expected reassigned owner, got %v", show.Meta["owner"])
	}
	if len(show.Notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(show.Notes))
	}

	// doing -> review is a valid transition.
	ok, err = svc.Status(ctx, id, "review", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected doing->review to be a valid transition")
	}

	// review -> doing is not a defined transition and must be rejected
	// without erroring (docs/features/task.md「ステータス機械」).
	ok, err = svc.Status(ctx, id, "doing", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected review->doing to be rejected")
	}
}

func TestTaskListAndFilter(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id1, err := svc.NewTask(ctx, "t1", "", "owner1@example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.NewTask(ctx, "t2", "", "owner2@example.com", "", ""); err != nil {
		t.Fatal(err)
	}

	all, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(all))
	}

	filtered, err := svc.List(ctx, ListFilter{Owner: "owner1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0]["id"] != id1 {
		t.Errorf("unexpected filtered list: %+v", filtered)
	}

	mine, err := svc.List(ctx, ListFilter{Mine: true, ActorEmail: "owner2@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0]["owner"] != "owner2@example.com" {
		t.Errorf("unexpected mine filter result: %+v", mine)
	}
}

func TestTaskResolveIDAndNotFound(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewTask(ctx, "t", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	full, err := svc.ResolveID(ctx, id[:10])
	if err != nil || full != id {
		t.Fatalf("prefix resolve failed: %v %v", full, err)
	}
	if _, err := svc.Show(ctx, "01j8x0a2b3c4d5e6f7g8h9j0ka"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestTaskLink(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewTask(ctx, "t", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, id, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", false); err != nil {
		t.Fatal(err)
	}
	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	links, _ := show.Meta["links"].([]any)
	if len(links) != 1 {
		t.Errorf("expected 1 link, got %v", links)
	}
}
