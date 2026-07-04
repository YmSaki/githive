package notifyapp

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

func newTestRepo(t *testing.T, email string) *Service {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", email}, {"user.name", email}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return New(dir)
}

func TestPostAndListUnread(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t, "a@example.com")
	ctx := context.Background()

	id1, err := svc.Post(ctx, []string{"user:b@example.com"}, "for b", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := svc.Post(ctx, []string{"all"}, "for everyone", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Post(ctx, []string{"user:c@example.com"}, "for c only", "", nil, ""); err != nil {
		t.Fatal(err)
	}

	all, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 posts, got %d", len(all))
	}

	unreadForB, err := svc.List(ctx, ListFilter{UnreadOnly: true, ActorEmail: "b@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadForB) != 2 {
		t.Fatalf("expected 2 unread posts for b (direct + all), got %d: %+v", len(unreadForB), unreadForB)
	}

	// b acks id1: this requires acting as b, since actor is resolved from
	// the repo's git identity (ADR-0009), so switch the repo's identity to
	// b before writing the ack event.
	setIdentity(t, svc.Dir, "b@example.com")
	if err := svc.Ack(ctx, []string{id1}); err != nil {
		t.Fatal(err)
	}
	unreadForB, err = svc.List(ctx, ListFilter{UnreadOnly: true, ActorEmail: "b@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadForB) != 1 || unreadForB[0]["id"] != id2 {
		t.Fatalf("expected only the 'all' post to remain unread, got %+v", unreadForB)
	}
}

func setIdentity(t *testing.T, dir, email string) {
	t.Helper()
	for _, kv := range [][2]string{{"user.email", email}, {"user.name", email}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
}

func TestAckIsIdempotent(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t, "a@example.com")
	ctx := context.Background()

	id, err := svc.Post(ctx, []string{"all"}, "t", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Ack(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Ack(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	unread, err := svc.List(ctx, ListFilter{UnreadOnly: true, ActorEmail: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Errorf("expected no unread after double-ack, got %+v", unread)
	}
}
