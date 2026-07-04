package chatapp

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

func TestChatThreadLifecycle(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewThread(ctx, "リリース手順の相談", "まず手順を書きます")
	if err != nil {
		t.Fatal(err)
	}

	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["status"] != "open" {
		t.Errorf("expected open, got %v", show.Meta["status"])
	}
	if len(show.Messages) != 1 || show.Messages[0]["body"] != "まず手順を書きます" {
		t.Errorf("expected create body to become first message, got %+v", show.Messages)
	}

	if err := svc.Post(ctx, id, "了解です", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Archive(ctx, id); err != nil {
		t.Fatal(err)
	}

	show, err = svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["status"] != "archived" {
		t.Errorf("expected archived, got %v", show.Meta["status"])
	}
	if len(show.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(show.Messages))
	}
	if show.Meta["message_count"] != 2 {
		t.Errorf("expected message_count 2, got %v", show.Meta["message_count"])
	}
}

func TestChatListAndFilter(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id1, err := svc.NewThread(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := svc.NewThread(ctx, "t2", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Archive(ctx, id2); err != nil {
		t.Fatal(err)
	}

	all, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(all))
	}

	open, err := svc.List(ctx, ListFilter{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0]["id"] != id1 {
		t.Errorf("unexpected open filter result: %+v", open)
	}
}

func TestChatResolveIDAndNotFound(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewThread(ctx, "t", "")
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
