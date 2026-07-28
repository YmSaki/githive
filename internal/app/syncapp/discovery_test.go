package syncapp

import (
	"context"
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
