package syncapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
)

// gitTrace records the git commands githive runs, by pointing GIT_TRACE at a
// file. gitx starts every subprocess with the test process's environment, so
// this observes the real production path without a test-only seam in it.
type gitTrace struct {
	t    *testing.T
	path string
}

func newGitTrace(t *testing.T) *gitTrace {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git-trace.log")
	t.Setenv("GIT_TRACE", path)
	return &gitTrace{t: t, path: path}
}

// reset drops everything recorded so far, so a measurement covers only the
// call that follows it rather than the fixture setup before it.
func (g *gitTrace) reset() {
	g.t.Helper()
	if err := os.WriteFile(g.path, nil, 0o600); err != nil {
		g.t.Fatal(err)
	}
}

// commands returns the argv of each recorded git invocation.
func (g *gitTrace) commands() [][]string {
	g.t.Helper()
	raw, err := os.ReadFile(g.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		g.t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(string(raw), "\n") {
		idx := strings.Index(line, "trace: ")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("trace: "):]
		for _, prefix := range []string{"built-in: ", "run_command: "} {
			if body, ok := strings.CutPrefix(rest, prefix); ok {
				if fields := strings.Fields(body); len(fields) > 0 && fields[0] == "git" {
					out = append(out, fields[1:])
				}
				break
			}
		}
	}
	return out
}

// subcommands returns the recorded invocations of one git subcommand, each as
// its full argv.
func (g *gitTrace) subcommands(name string) [][]string {
	var out [][]string
	for _, argv := range g.commands() {
		for _, a := range argv {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if a == name {
				out = append(out, argv)
			}
			break
		}
	}
	return out
}

// pushes returns only the pushes githive itself made. gitx always passes
// --porcelain, which distinguishes them from any push a test fixture's git
// hook performs (those inherit GIT_TRACE too).
func (g *gitTrace) pushes() [][]string {
	var out [][]string
	for _, argv := range g.subcommands("push") {
		for _, a := range argv {
			if a == "--porcelain" {
				out = append(out, argv)
				break
			}
		}
	}
	return out
}

func seedIssues(t *testing.T, dir string, n int) []string {
	t.Helper()
	ctx := context.Background()
	svc := issueapp.New(dir)
	refs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := svc.NewIssue(ctx, "seed", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, "refs/projects/issue/"+id)
	}
	return refs
}

// TestSyncBatchesFetchAndPush is docs/03-sync-and-concurrency.md's
// 「全 ref 一括の sync は、fetch を 1 回のワイルドカード fetch に、push を
// 複数 refspec の 1 回の git push にまとめる」: three refs must cost one
// fetch and one push, not one of each per ref.
func TestSyncBatchesFetchAndPush(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	refs := seedIssues(t, dir, 3)

	trace := newGitTrace(t)
	trace.reset()
	results, err := Sync(context.Background(), dir, "origin", refs, 5)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %+v", results)
	}

	if got := len(trace.subcommands("fetch")); got != 1 {
		t.Errorf("expected 1 fetch for 3 refs, got %d", got)
	}
	pushes := trace.pushes()
	if len(pushes) != 1 {
		t.Fatalf("expected 1 push for 3 refs, got %d", len(pushes))
	}
	if got := countRefspecs(pushes[0]); got != 3 {
		t.Errorf("expected the single push to carry 3 refspecs, got %d in %v", got, pushes[0])
	}
}

// TestSyncSkipInitialFetchReusesDiscoveryFetch: DiscoverRefs already fetched
// the namespace, so the first round must not repeat it.
func TestSyncSkipInitialFetchReusesDiscoveryFetch(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	refs := seedIssues(t, dir, 2)
	ctx := context.Background()

	trace := newGitTrace(t)
	trace.reset()
	if _, err := Sync(ctx, dir, "origin", refs, 5, SkipInitialFetch()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := len(trace.subcommands("fetch")); got != 0 {
		t.Errorf("expected no fetch when the caller already fetched, got %d", got)
	}
}

// TestSyncRetriesOnlyRejectedRefs is the per-ref retry docs/03 asks for:
// when only one ref of a batch loses the push race, the next round must
// re-process that ref alone rather than redoing the whole batch.
//
// Losing the race requires the remote to move between our fetch and our
// push, which batching otherwise makes rare - a round's fetch is immediately
// followed by its push. A pre-push hook in the pushing clone creates that
// window deterministically by letting the other clone push first.
func TestSyncRetriesOnlyRejectedRefs(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// One shared issue both clones know about.
	shared := seedIssues(t, dirA, 1)[0]
	if _, err := Sync(ctx, dirA, "origin", []string{shared}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirB, "origin", []string{shared}, 5); err != nil {
		t.Fatal(err)
	}
	sharedID := strings.TrimPrefix(shared, "refs/projects/issue/")

	// Both write to it. A's write stays local for now; the hook below pushes
	// it at exactly the wrong moment for B.
	if err := issueapp.New(dirA).Label(ctx, sharedID, []string{"from-a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := issueapp.New(dirB).Label(ctx, sharedID, []string{"from-b"}, nil); err != nil {
		t.Fatal(err)
	}
	own := seedIssues(t, dirB, 2)
	installRacingPrePushHook(t, dirB, dirA, shared)

	refs := append([]string{shared}, own...)
	trace := newGitTrace(t)
	trace.reset()
	results, err := Sync(ctx, dirB, "origin", refs, 5)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected all 3 refs to converge, got %+v", results)
	}

	pushes := trace.pushes()
	if len(pushes) != 2 {
		t.Fatalf("expected 2 pushes (batch, then the rejected ref alone), got %d: %v", len(pushes), pushes)
	}
	if got := countRefspecs(pushes[0]); got != 3 {
		t.Errorf("first push should carry all 3 refspecs, got %d", got)
	}
	if got := countRefspecs(pushes[1]); got != 1 {
		t.Errorf("retry push should carry only the rejected ref, got %d: %v", got, pushes[1])
	}

	// Both sides' labels survive the merge the retry round performed.
	shown, err := issueapp.New(dirB).Show(ctx, sharedID)
	if err != nil {
		t.Fatal(err)
	}
	labels := strings.Join(anySliceToStrings(shown.Meta["labels"]), ",")
	for _, want := range []string{"from-a", "from-b"} {
		if !strings.Contains(labels, want) {
			t.Errorf("expected label %q to survive, got %q", want, labels)
		}
	}
}

// installRacingPrePushHook makes the first push out of pusherDir lose the
// race for ref: the hook pushes peerDir's version of ref to the shared
// remote first, so the push it is running ahead of is rejected
// non-fast-forward. It fires once, so the retry round can succeed.
//
// The peer path and ref go into files rather than into the script text: the
// script runs under /bin/sh, and on Windows an embedded path's backslashes
// would be read as escapes.
func installRacingPrePushHook(t *testing.T, pusherDir, peerDir, ref string) {
	t.Helper()
	hooksDir := filepath.Join(pusherDir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pusherDir, ".git", "race-peer"), []byte(filepath.ToSlash(peerDir)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pusherDir, ".git", "race-ref"), []byte(ref+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ -f .git/race-done ]; then exit 0; fi\n" +
		": > .git/race-done\n" +
		"git -C \"$(cat .git/race-peer)\" push -q origin \"$(cat .git/race-ref):$(cat .git/race-ref)\" >/dev/null 2>&1\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-push"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestSyncPermanentRejectionDoesNotBlockOtherRefs: a ref the remote refuses
// outright must fail on its own without aborting the rest of the batch
// (docs/01-architecture.md「部分的成功は正常系として報告する」).
func TestSyncPermanentRejectionDoesNotBlockOtherRefs(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	refs := seedIssues(t, dir, 3)

	// An `update` hook runs once per ref, so it can refuse exactly one while
	// the others are accepted - unlike pre-receive, which is all-or-nothing.
	installRefRejectingHook(t, origin, refs[1])

	results, err := Sync(context.Background(), dir, "origin", refs, 5)
	if err == nil {
		t.Fatal("expected the refused ref to surface as an error")
	}
	if !errors.Is(err, ErrPushRejected) {
		t.Errorf("expected ErrPushRejected, got %v", err)
	}
	if errors.Is(err, ErrRetriesExhausted) {
		t.Errorf("a permanent rejection must not be reported as retries exhausted: %v", err)
	}
	if !strings.Contains(err.Error(), refs[1]) {
		t.Errorf("expected the error to name the refused ref %s: %v", refs[1], err)
	}
	if len(results) != 2 {
		t.Fatalf("expected the other 2 refs to complete, got %+v", results)
	}
	for _, res := range results {
		if res.Ref == refs[1] {
			t.Errorf("the refused ref should not be reported as synced: %+v", res)
		}
	}
}

// installRefRejectingHook writes an `update` hook into the bare repo that
// refuses exactly one ref. The rejected ref name is written to a file next to
// the hook rather than embedded in the script, so the script needs no
// quoting and stays valid on Windows too (git runs hooks with the bare
// repository as their working directory).
func installRefRejectingHook(t *testing.T, bareDir, ref string) {
	t.Helper()
	hooksDir := filepath.Join(bareDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bareDir, "rejected-ref"), []byte(ref+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"$(cat rejected-ref)\" ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "update"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// countRefspecs counts the `<from>:<to>` arguments of a recorded git push.
func countRefspecs(argv []string) int {
	n := 0
	for _, a := range argv {
		if strings.Contains(a, ":") && strings.HasPrefix(a, "refs/") {
			n++
		}
	}
	return n
}

func anySliceToStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestSyncOrdersResultsAsGiven pins the contract the CLI's output relies on.
func TestSyncOrdersResultsAsGiven(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	refs := seedIssues(t, dir, 3)

	results, err := Sync(context.Background(), dir, "origin", refs, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(refs) {
		t.Fatalf("expected %d results, got %d", len(refs), len(results))
	}
	for i, res := range results {
		if res.Ref != refs[i] {
			t.Errorf("result %d is %s, want %s", i, res.Ref, refs[i])
		}
	}
}
