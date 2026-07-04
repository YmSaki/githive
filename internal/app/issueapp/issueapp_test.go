package issueapp

import (
	"context"
	"os/exec"
	"sync"
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

func TestNewShowLifecycle(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewIssue(ctx, "sync が Windows でパスを壊す", "再現手順...", []string{"bug"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isULID(id) {
		t.Fatalf("expected a ULID id, got %q", id)
	}

	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["title"] != "sync が Windows でパスを壊す" {
		t.Errorf("unexpected title: %v", show.Meta["title"])
	}
	if show.Body != "再現手順..." {
		t.Errorf("unexpected body: %q", show.Body)
	}
	if show.Meta["status"] != "open" {
		t.Errorf("unexpected status: %v", show.Meta["status"])
	}

	if err := svc.Comment(ctx, id, "LGTM", "", ""); err != nil {
		t.Fatal(err)
	}
	ok, err := svc.Status(ctx, id, "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected valid transition open->in_progress to succeed")
	}

	show, err = svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Comments) != 1 || show.Comments[0]["body"] != "LGTM" {
		t.Errorf("unexpected comments: %+v", show.Comments)
	}
	if show.Meta["status"] != "in_progress" {
		t.Errorf("expected in_progress, got %v", show.Meta["status"])
	}

	// Invalid transition: should not error, but should report ok=false and
	// leave status unchanged (docs/features/issue.md「ステータス機械」).
	ok, err = svc.Status(ctx, id, "archived")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected in_progress->archived to be rejected")
	}
	show, err = svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if show.Meta["status"] != "in_progress" {
		t.Errorf("status should be unchanged after invalid transition, got %v", show.Meta["status"])
	}
}

func TestLabelAssignLink(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewIssue(ctx, "t", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Label(ctx, id, []string{"bug", "p1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Assign(ctx, id, []string{"a@example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, id, "task", "01j8x0a2b3c4d5e6f7g8h9j0ka", false); err != nil {
		t.Fatal(err)
	}

	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := show.Meta["labels"].([]any)
	if len(labels) != 2 {
		t.Errorf("expected 2 labels, got %v", labels)
	}
	assignees, _ := show.Meta["assignees"].([]any)
	if len(assignees) != 1 || assignees[0] != "a@example.com" {
		t.Errorf("unexpected assignees: %v", assignees)
	}
	links, _ := show.Meta["links"].([]any)
	if len(links) != 1 {
		t.Errorf("unexpected links: %v", links)
	}
}

func TestListAndFilter(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id1, err := svc.NewIssue(ctx, "first", "", []string{"bug"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.NewIssue(ctx, "second", "", []string{"feature"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	all, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(all))
	}

	bugsOnly, err := svc.List(ctx, ListFilter{Label: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bugsOnly) != 1 || bugsOnly[0]["id"] != id1 {
		t.Errorf("unexpected filtered list: %+v", bugsOnly)
	}
}

func TestResolveIDPrefixAndAmbiguity(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()

	id, err := svc.NewIssue(ctx, "t", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	full, err := svc.ResolveID(ctx, id)
	if err != nil || full != id {
		t.Fatalf("full id resolve failed: %v %v", full, err)
	}
	prefix := id[:10]
	resolved, err := svc.ResolveID(ctx, prefix)
	if err != nil || resolved != id {
		t.Fatalf("prefix resolve failed: %v %v", resolved, err)
	}

	if _, err := svc.ResolveID(ctx, "01j8x0a2"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound for unrelated prefix, got %v", err)
	}
}

func TestNotFound(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if _, err := svc.Show(ctx, "01j8x0a2b3c4d5e6f7g8h9j0ka"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestIdentityRequired(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Isolate from any ambient global/system gitconfig (this machine's
	// global config sets user.email) so the repo genuinely has none set.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	svc := New(dir)
	if _, err := svc.NewIssue(context.Background(), "t", "", nil, nil); err != ErrIdentityNotConfigured {
		t.Errorf("expected ErrIdentityNotConfigured, got %v", err)
	}
}

// TestConcurrentLocalWrites exercises the CAS retry loop: many goroutines
// commenting on the same issue concurrently must all succeed and all
// comments must survive (docs/14-testing.md シナリオ11「ローカル同時書き込み」).
func TestConcurrentLocalWrites(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	id, err := svc.NewIssue(ctx, "t", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.Comment(ctx, id, "c", "", "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	show, err := svc.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Comments) != n {
		t.Errorf("expected %d comments to survive concurrent writes, got %d", n, len(show.Comments))
	}
}

func isULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}
