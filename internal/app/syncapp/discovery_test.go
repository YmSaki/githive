package syncapp

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// TestDiscoverRefsFindsRemoteOnlyEntities is the regression this logic exists
// for: an entity another clone created lives only in the remote, so
// enumerating local refs alone would make `githive sync` unable to ever
// discover it - it would report nothing to do, forever. DiscoverRefs fetches
// first and unions the local refs with whatever appeared under
// refs/githive-remote/* (ADR-0008).
//
// This moved down from cmd/githive when the logic moved into syncapp (B2);
// the CLI keeps its own end-to-end test of the same scenario.
func TestDiscoverRefsFindsRemoteOnlyEntities(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// A creates an issue and a notify post, and pushes both.
	id, err := issueapp.New(dirA).NewIssue(ctx, "b has never seen this", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	issueRef := "refs/projects/issue/" + id
	if _, err := notifyapp.New(dirA).Post(ctx, []string{"user:b@example.com"}, "hi", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{issueRef, notifyapp.StreamRef}, 5); err != nil {
		t.Fatal(err)
	}

	// B has neither ref locally.
	if oid := mustRevParse(t, dirB, issueRef); oid != "" {
		t.Fatalf("fixture broken: B already has %s", issueRef)
	}

	refs, err := DiscoverRefs(ctx, dirB, "origin", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{issueRef, notifyapp.StreamRef} {
		if !containsRef(refs, want) {
			t.Errorf("DiscoverRefs did not find remote-only ref %s: %v", want, refs)
		}
	}
}

// TestDiscoverRefsHonoursKindFilter covers `githive sync --kind`.
func TestDiscoverRefsHonoursKindFilter(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()

	id, err := issueapp.New(dir).NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifyapp.New(dir).Post(ctx, []string{"user:a@example.com"}, "hi", "", nil, ""); err != nil {
		t.Fatal(err)
	}

	refs, err := DiscoverRefs(ctx, dir, "origin", []string{"issue"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRef(refs, "refs/projects/issue/"+id) {
		t.Errorf("expected the issue ref, got %v", refs)
	}
	if containsRef(refs, notifyapp.StreamRef) {
		t.Errorf("--kind issue should exclude the notify stream, got %v", refs)
	}
}

// TestUnpushedRefsComparesAgainstTracking pins what `githive status` reports.
func TestUnpushedRefsComparesAgainstTracking(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()

	id, err := issueapp.New(dir).NewIssue(ctx, "seed", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id

	unpushed, err := UnpushedRefs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRef(unpushed, ref) {
		t.Fatalf("a never-pushed ref should be reported: %v", unpushed)
	}

	if _, err := Sync(ctx, dir, "origin", []string{ref}, 5); err != nil {
		t.Fatal(err)
	}
	// UnpushedRefs deliberately does not fetch, so its answer is relative to
	// the last fetch: the push itself does not advance refs/githive-remote/*.
	// A second sync fetches and brings the tracking ref up to date.
	if _, err := Sync(ctx, dir, "origin", []string{ref}, 5); err != nil {
		t.Fatal(err)
	}
	unpushed, err = UnpushedRefs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if containsRef(unpushed, ref) {
		t.Errorf("a pushed-and-refetched ref should no longer be reported: %v", unpushed)
	}
}

func mustRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	oid, err := gitx.New(dir).RevParse(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// A remote deletion or migration must not be hidden by stale tracking refs.
// Discovery updates status without deleting the authoritative local chain;
// direct Sync must perform the same repair when discovery is not called.
func TestSyncRepairsMissingRemoteRefs(t *testing.T) {
	requireGit(t)
	for _, discover := range []bool{false, true} {
		for _, migrate := range []bool{false, true} {
			t.Run(fmt.Sprintf("discover=%t/migrate=%t", discover, migrate), func(t *testing.T) {
				ctx := context.Background()
				origin := initBareOrigin(t)
				dir := cloneRepo(t, origin, "a@example.com")
				// Explicitly override a user's global setting: repair cannot depend on it.
				if err := gitx.New(dir).ConfigSet(ctx, "fetch.prune", "false"); err != nil {
					t.Fatal(err)
				}
				refs := seedIssues(t, dir, 2)
				for i := 0; i < 2; i++ {
					if _, err := Sync(ctx, dir, "origin", refs, 0); err != nil {
						t.Fatal(err)
					}
				}
				expected := map[string]string{}
				for _, ref := range refs {
					expected[ref] = mustRevParse(t, dir, ref)
				}
				if migrate {
					origin = initBareOrigin(t)
					out, err := exec.Command("git", "-C", dir, "remote", "set-url", "origin", origin).CombinedOutput()
					if err != nil {
						t.Fatalf("set-url: %v %s", err, out)
					}
				} else {
					for _, ref := range refs {
						if err := gitx.New(origin).DeleteRef(ctx, ref, expected[ref]); err != nil {
							t.Fatal(err)
						}
					}
				}
				opts := []Option{}
				if discover {
					found, err := DiscoverRefs(ctx, dir, "origin", nil)
					if err != nil {
						t.Fatal(err)
					}
					unpushed, err := UnpushedRefs(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, ref := range refs {
						if !containsRef(found, ref) || !containsRef(unpushed, ref) {
							t.Fatalf("missing local ref: found=%v unpushed=%v", found, unpushed)
						}
						if got := mustRevParse(t, dir, ref); got != expected[ref] {
							t.Fatalf("local ref changed: got %s want %s", got, expected[ref])
						}
					}
					opts = append(opts, SkipInitialFetch())
				}
				results, err := Sync(ctx, dir, "origin", refs, 0, opts...)
				if err != nil {
					t.Fatal(err)
				}
				if len(results) != len(refs) {
					t.Fatalf("results: %+v", results)
				}
				for _, res := range results {
					if res.Action != ActionFastForwardOut {
						t.Fatalf("expected re-push: %+v", res)
					}
				}
				for _, ref := range refs {
					if got := mustRevParse(t, origin, ref); got != expected[ref] {
						t.Fatalf("remote ref %s: got %s want %s", ref, got, expected[ref])
					}
				}
			})
		}
	}
}
