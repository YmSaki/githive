package syncapp

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// FetchSnapshot returns exactly the fetched remote view and leaves no
// private refs behind (ADR-0035).
func TestFetchSnapshotMatchesRemoteAndCleansUp(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	a := cloneRepo(t, origin, "a@example.com")
	b := cloneRepo(t, origin, "b@example.com")
	cfg := metaConflictFixture(t, a, "p", 1, false)
	gitRun(t, a, "update-ref", refspace.MetaConfigRef, cfg)
	gitRun(t, a, "push", "--quiet", "origin", refspace.MetaConfigRef+":"+refspace.MetaConfigRef)
	snap, err := FetchSnapshot(context.Background(), gitx.New(b), "origin")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 || snap[refspace.MetaConfigRef] != cfg {
		t.Fatalf("snapshot %v", snap)
	}
	if left := gitRun(t, b, "for-each-ref", snapshotRoot+"/"); left != "" {
		t.Fatalf("private refs left: %s", left)
	}
	if tracking := refOID(t, b, "refs/githive-remote/meta/config"); tracking != cfg {
		t.Fatalf("tracking ref not updated: %s", tracking)
	}
}

// §11-5 (h)(i): within a round, the schema check and planning use the
// round's own fetch result, not refs/githive-remote/* that another process
// may have overwritten with a different remote's state.
func TestRoundSnapshotOverridesTrackingRefs(t *testing.T) {
	requireGit(t)
	dir := cloneRepo(t, initBareOrigin(t), "a@example.com")
	ctx := context.Background()
	local := metaConflictFixture(t, dir, "p", 1, false)
	future := metaConflictFixture(t, dir, "p", 99, false)
	gitRun(t, dir, "update-ref", refspace.MetaConfigRef, local)
	// Another remote B (supported version, same as local) is what the
	// tracking ref shows; remote A's fetch result is the future version.
	gitRun(t, dir, "update-ref", "refs/githive-remote/meta/config", local)

	var unsupported *entitychain.SchemaUnsupportedError
	if err := checkSchemaBothSides(ctx, dir, RemoteSnapshot{refspace.MetaConfigRef: future}); !errors.As(err, &unsupported) {
		t.Fatalf("schema check used tracking state: %v", err)
	}
	// The tracking ref is never consulted: a future version there is
	// ignored when the snapshot says the remote lacks meta/config.
	gitRun(t, dir, "update-ref", "refs/githive-remote/meta/config", future)
	if err := checkSchemaBothSides(ctx, dir, RemoteSnapshot{}); err != nil {
		t.Fatalf("schema check read the tracking ref: %v", err)
	}
	gitRun(t, dir, "update-ref", "refs/githive-remote/meta/config", local)

	// (i): remote A lacks the ref; B's tracking copy equals local. Planning
	// from A's snapshot must push instead of reporting up-to-date.
	r := gitx.New(dir)
	plan, res, err := planRef(ctx, dir, r, refspace.MetaConfigRef, RemoteSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.action != ActionFastForwardOut {
		t.Fatalf("plan %+v res %+v", plan, res)
	}
	if _, res, err := planRef(ctx, dir, r, refspace.MetaConfigRef, RemoteSnapshot{refspace.MetaConfigRef: local}); err != nil || res.Action != ActionUpToDate {
		t.Fatalf("snapshot equal to local: %+v %v", res, err)
	}
}

// The SkipInitialFetch first round plans from DiscoverRefs' own fetch: a
// concurrent fetch of another remote that rewrites refs/githive-remote/*
// right after discovery must not turn a needed push into up-to-date.
func TestDiscoverySnapshotProtectsFirstRoundPlanning(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()
	ref := seedIssues(t, dir, 1)[0]
	localOID := refOID(t, dir, ref)

	refs, snap, err := DiscoverRefs(ctx, dir, "origin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRef(refs, ref) || snap[ref] != "" {
		t.Fatalf("discovery: refs=%v snap=%v", refs, snap)
	}
	// Another remote B that already has the ref is fetched concurrently.
	tracking, err := refspace.RemoteTrackingRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "update-ref", tracking, localOID)

	results, err := Sync(ctx, dir, "origin", refs, 0, SkipInitialFetch(snap))
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if res.Ref == ref && res.Action != ActionFastForwardOut {
			t.Fatalf("first round used the tracking ref: %+v", res)
		}
	}
	if got := refOID(t, origin, ref); got != localOID {
		t.Fatalf("origin %s: got %q want %s", ref, got, localOID)
	}
}

// Discovery enumerates candidates from its own fetch result, and the first
// round's schema check uses that result's meta/config even after another
// remote's fetch rewrote the tracking ref.
func TestDiscoverySnapshotProtectsFirstRoundSchemaAndCandidates(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	ctx := context.Background()
	local := metaConflictFixture(t, dir, "p", 1, false)
	future := metaConflictFixture(t, dir, "p", 99, false)
	gitRun(t, dir, "update-ref", refspace.MetaConfigRef, local)
	gitRun(t, dir, "push", "--quiet", "origin", future+":"+refspace.MetaConfigRef)
	// A remote-only entity appears in origin; its tracking copy will be
	// pruned by the concurrent fetch below.
	other := cloneRepo(t, origin, "b@example.com")
	remoteOnly := seedIssues(t, other, 1)[0]
	gitRun(t, other, "push", "--quiet", "origin", remoteOnly+":"+remoteOnly)

	// The concurrent fetch lands between discovery's fetch and its listing.
	tracking, _ := refspace.RemoteTrackingRef(remoteOnly)
	afterDiscoveryFetch = func() {
		gitRun(t, dir, "update-ref", "-d", tracking)
		gitRun(t, dir, "update-ref", "refs/githive-remote/meta/config", local)
	}
	t.Cleanup(func() { afterDiscoveryFetch = nil })
	refs, snap, err := DiscoverRefs(ctx, dir, "origin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRef(refs, remoteOnly) || snap[refspace.MetaConfigRef] != future {
		t.Fatalf("candidates did not come from the discovery fetch: refs=%v snap=%v", refs, snap)
	}

	var unsupported *entitychain.SchemaUnsupportedError
	if _, err := Sync(ctx, dir, "origin", refs, 0, SkipInitialFetch(snap)); !errors.As(err, &unsupported) {
		t.Fatalf("first-round schema check used the tracking ref: %v", err)
	}
}
