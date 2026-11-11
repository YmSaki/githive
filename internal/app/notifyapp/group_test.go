package notifyapp

import (
	"context"
	"os/exec"
	"testing"
)

// TestListUnreadGroupAndOtherEmails: with a membership resolver, posts to
// the actor's groups and to the actor's other registry emails are unread,
// and an ack by any of the actor's emails clears them (#137).
func TestListUnreadGroupAndOtherEmails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	svc := New(newAutoTestRepo(t))
	ctx := context.Background()
	for _, targets := range [][]string{{"group:core"}, {"group:ops"}, {"user:alt@example.com"}, {"user:other@example.com"}} {
		if _, err := svc.Post(ctx, targets, "t", "", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	resolver := func(ctx context.Context, email string) ([]string, []string, error) {
		return []string{email, "alt@example.com"}, []string{"core"}, nil
	}
	filter := ListFilter{UnreadOnly: true, ActorEmail: "me@example.com", Membership: resolver}
	posts, err := svc.List(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("want group:core and user:alt unread, got %v", posts)
	}

	// Without a resolver, only direct/all targets count.
	if posts, _ := svc.List(ctx, ListFilter{UnreadOnly: true, ActorEmail: "me@example.com"}); len(posts) != 0 {
		t.Fatalf("without membership nothing is addressed to me, got %v", posts)
	}
}
