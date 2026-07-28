package syncapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/core/gitx"
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

// TestClassifyPushRejection pins classifyPushRejection's non-fast-forward
// vs. permanent-rejection split (A4, docs/10-cli-spec.md「終了コード」): the
// races a fetch->merge->push retry can resolve map to gitx.ErrNonFastForward,
// every other rejection - including a git version's phrasing this function
// does not recognize - maps to ErrPushRejected carrying the raw reason.
func TestClassifyPushRejection(t *testing.T) {
	cases := []struct {
		name      string
		reason    string
		wantNonFF bool
	}{
		{"non-fast-forward", "[rejected] (non-fast-forward)", true},
		{"fetch first", "[rejected] (fetch first)", true},
		{"stale info", "[rejected] (stale info)", true},
		{"pre-receive hook", "[remote rejected] (pre-receive hook declined)", false},
		{"unrecognized reason falls back to permanent", "[remote rejected] (some future git message)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyPushRejection(c.reason)
			if c.wantNonFF {
				if !errors.Is(err, gitx.ErrNonFastForward) {
					t.Errorf("classifyPushRejection(%q) = %v, want gitx.ErrNonFastForward", c.reason, err)
				}
				return
			}
			if !errors.Is(err, ErrPushRejected) {
				t.Errorf("classifyPushRejection(%q) = %v, want ErrPushRejected", c.reason, err)
			}
			if !strings.Contains(err.Error(), c.reason) {
				t.Errorf("classifyPushRejection(%q).Error() = %q, want it to contain the raw git reason", c.reason, err.Error())
			}
		})
	}
}

// installRejectingHook writes a pre-receive hook into the bare repo at
// bareDir that rejects every push and records one line to a counter file per
// invocation, so tests can assert exactly how many times the remote saw a
// push attempt.
func installRejectingHook(t *testing.T, bareDir string) {
	t.Helper()
	hooksDir := filepath.Join(bareDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(bareDir, "pre-receive-hits")
	script := "#!/bin/sh\necho hit >> " + counter + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// hookHitCount reads back how many times installRejectingHook's script ran.
func hookHitCount(t *testing.T, bareDir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(bareDir, "pre-receive-hits"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// TestSyncPushRejectedByPreReceiveHookFailsOnce is A4's headline scenario: a
// remote that permanently rejects every push (a pre-receive hook, standing
// in for policy/permission rejections in general) must fail sync's very
// first attempt with ErrPushRejected instead of retrying up to Retries times
// like a real non-fast-forward race would.
func TestSyncPushRejectedByPreReceiveHookFailsOnce(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	installRejectingHook(t, origin)
	dirA := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()

	svcA := issueapp.New(dirA)
	id, err := svcA.NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id

	_, err = Sync(ctx, dirA, "origin", []string{ref}, 5)
	if err == nil {
		t.Fatal("expected the pre-receive hook's rejection to surface as an error")
	}
	if !errors.Is(err, ErrPushRejected) {
		t.Errorf("expected ErrPushRejected, got: %v", err)
	}
	if errors.Is(err, ErrRetriesExhausted) {
		t.Errorf("a permanent rejection must not be reported as retries exhausted: %v", err)
	}
	if got := hookHitCount(t, origin); got != 1 {
		t.Errorf("expected exactly 1 push attempt against a permanently-rejecting remote, got %d", got)
	}
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

// TestRetriesFromConfig verifies githive.sync.retries wiring
// (docs/10-cli-spec.md「設定（git config）」: 既定 5, 0 以上の整数のみ受理):
// unset, negative, and unparsable values all fall back to DefaultRetries,
// while a valid non-negative integer (including 0) overrides it.
func TestRetriesFromConfig(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	ctx := context.Background()

	if got := RetriesFromConfig(ctx, dir); got != DefaultRetries {
		t.Errorf("unset config: got %d, want DefaultRetries (%d)", got, DefaultRetries)
	}

	setConfig := func(v string) {
		t.Helper()
		if out, err := exec.Command("git", "-C", dir, "config", "githive.sync.retries", v).CombinedOutput(); err != nil {
			t.Fatalf("git config githive.sync.retries %q: %v\n%s", v, err, out)
		}
	}

	setConfig("3")
	if got := RetriesFromConfig(ctx, dir); got != 3 {
		t.Errorf("githive.sync.retries=3: got %d, want 3", got)
	}

	setConfig("0")
	if got := RetriesFromConfig(ctx, dir); got != 0 {
		t.Errorf("githive.sync.retries=0: got %d, want 0 (0 is a valid non-negative integer)", got)
	}

	setConfig("-1")
	if got := RetriesFromConfig(ctx, dir); got != DefaultRetries {
		t.Errorf("githive.sync.retries=-1 (negative): got %d, want DefaultRetries (%d)", got, DefaultRetries)
	}

	setConfig("not-a-number")
	if got := RetriesFromConfig(ctx, dir); got != DefaultRetries {
		t.Errorf("githive.sync.retries=not-a-number (unparsable): got %d, want DefaultRetries (%d)", got, DefaultRetries)
	}
}
