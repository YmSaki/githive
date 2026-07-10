package logapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
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

// seedEvents creates one event in each of issue/task/chat/notify/users so
// the timeline has cross-feature entries to merge.
func seedEvents(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()

	if _, err := issueapp.New(dir).NewIssue(ctx, "issue1", "", nil, nil); err != nil {
		t.Fatalf("NewIssue: %v", err)
	}
	if _, err := taskapp.New(dir).NewTask(ctx, "task1", "", "", "", ""); err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if _, err := chatapp.New(dir).NewThread(ctx, "chat1", ""); err != nil {
		t.Fatalf("NewThread: %v", err)
	}
	if _, err := notifyapp.New(dir).Post(ctx, []string{"all"}, "notify1", "", nil, ""); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if err := usersapp.New(dir).AddUser(ctx, "user1", "", "user1@example.com", "human", nil); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
}

func TestListMergesAcrossFeatures(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	seedEvents(t, dir)
	ctx := context.Background()

	entries, err := New(dir).List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	// issue.create, task.create, chat.create, notify.post, users.user_set: 5
	// events minimum (notify.post may itself trigger no further auto-events
	// here, since there is no assignee/mention to notify about beyond the
	// explicit notifyapp.Post call above).
	if len(entries) < 5 {
		t.Fatalf("expected at least 5 entries, got %d: %+v", len(entries), entries)
	}

	seenFeatures := map[string]bool{}
	for i, e := range entries {
		seenFeatures[e["feature"].(string)] = true
		if i > 0 && entries[i-1]["id"].(string) > e["id"].(string) {
			t.Fatalf("entries not sorted chronologically at index %d: %q > %q", i, entries[i-1]["id"], e["id"])
		}
	}
	for _, f := range []string{"issue", "task", "chat", "notify", "users"} {
		if !seenFeatures[f] {
			t.Errorf("expected an entry from feature %q, got features %v", f, seenFeatures)
		}
	}
}

func TestListFilterSince(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	seedEvents(t, dir)
	ctx := context.Background()

	all, err := New(dir).List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one entry to test filtering against")
	}

	// Since set to just after the last event's ts should yield nothing.
	future, err := New(dir).List(ctx, ListFilter{Since: "9999-01-01T00:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("expected 0 entries with a far-future Since, got %d", len(future))
	}

	// Since set to the earliest ts should yield everything.
	fromStart, err := New(dir).List(ctx, ListFilter{Since: all[0]["ts"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if len(fromStart) != len(all) {
		t.Errorf("expected %d entries with Since=earliest ts, got %d", len(all), len(fromStart))
	}
}

func TestListInvalidSince(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	seedEvents(t, dir)
	ctx := context.Background()

	// Each of these is RFC3339-valid but not the envelope ts format
	// (RFC3339 UTC millisecond precision, e.g. 2026-07-09T12:00:00.000Z):
	// second precision, and a non-Z UTC offset.
	invalid := []string{
		"not-a-timestamp",
		"2026-07-09T12:00:00Z",
		"2026-07-09T12:00:00.000+00:00",
	}
	for _, since := range invalid {
		_, err := New(dir).List(ctx, ListFilter{Since: since})
		if !errors.Is(err, ErrInvalidSince) {
			t.Errorf("List with Since=%q: expected ErrInvalidSince, got %v", since, err)
		}
	}
}

func TestListFilterActor(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	seedEvents(t, dir)
	ctx := context.Background()

	mine, err := New(dir).List(ctx, ListFilter{ActorEmail: "tester@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) == 0 {
		t.Fatal("expected entries for the configured actor")
	}

	nobody, err := New(dir).List(ctx, ListFilter{ActorEmail: "someone-else@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nobody) != 0 {
		t.Errorf("expected 0 entries for an unrelated actor, got %d", len(nobody))
	}
}

// TestListSkipsCheckpoints confirms List() honors checkpoint transparency
// (internal/core/materialize/materialize.go's universal ".checkpoint"-suffix
// skip, .claude/rules/determinism.md) even though it walks raw events rather
// than folding through materialize.Registry.
func TestListSkipsCheckpoints(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	issueID, err := issueapp.New(dir).NewIssue(ctx, "issue1", "", nil, nil)
	if err != nil {
		t.Fatalf("NewIssue: %v", err)
	}

	before, err := New(dir).List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := refspace.EntityRef(refspace.FeatureIssue, issueID)
	if err != nil {
		t.Fatal(err)
	}
	r := gitx.New(dir)
	oldOID, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := chain.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	cpEnv := &event.Envelope{
		V: 1, Kind: "issue.checkpoint", ID: "01j8xq4d3nbz9k7w2m5e8h1t99",
		TS: "2026-07-04T12:00:00.000Z", Actor: "tester@example.com",
		Entity: issueID, Data: map[string]any{"bogus": "should never surface"}, Extra: map[string]any{},
	}
	newHash, err := chain.AppendEvent(repo, plumbing.NewHash(oldOID), cpEnv, "issue.checkpoint", map[string][]byte{}, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dir, plumbing.ReferenceName(ref), newHash, oldOID); err != nil {
		t.Fatal(err)
	}

	after, err := New(dir).List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("expected checkpoint to be invisible to List(): before=%d after=%d", len(before), len(after))
	}
	for _, e := range after {
		if e["id"] == cpEnv.ID {
			t.Errorf("checkpoint event %q leaked into List() output", cpEnv.ID)
		}
	}
}
