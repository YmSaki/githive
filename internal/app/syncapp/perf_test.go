package syncapp

import (
	"context"
	"testing"
)

func TestSyncUpToDateAndUnpushedRefsAvoidPerRefRevParse(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	origin := initBareOrigin(t)
	dir := cloneRepo(t, origin, "a@example.com")
	refs := seedIssues(t, dir, 5)
	if _, err := Sync(ctx, dir, "origin", refs, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverRefs(ctx, dir, "origin", nil); err != nil {
		t.Fatal(err)
	}

	trace := newGitTrace(t)
	trace.reset()

	unpushed, err := UnpushedRefs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpushed) != 0 {
		t.Fatalf("got %d unpushed, want 0", len(unpushed))
	}
	if got := len(trace.subcommands("rev-parse")); got != 0 {
		t.Fatalf("UnpushedRefs ran rev-parse %d times, want 0", got)
	}
	if got := len(trace.subcommands("for-each-ref")); got != 1 {
		t.Fatalf("UnpushedRefs ran for-each-ref %d times, want 1", got)
	}

	trace.reset()
	results, err := Sync(ctx, dir, "origin", refs, 0, SkipInitialFetch())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Action != ActionUpToDate {
			t.Fatalf("ref %s action = %s, want %s", r.Ref, r.Action, ActionUpToDate)
		}
	}
	// Only checkSchemaBothSides (2 rev-parse calls) should run, independent of ref count (5).
	if got := len(trace.subcommands("rev-parse")); got != 2 {
		t.Fatalf("Sync up-to-date ran rev-parse %d times, want 2", got)
	}
	if got := len(trace.subcommands("for-each-ref")); got != 1 {
		t.Fatalf("Sync up-to-date ran for-each-ref %d times, want 1", got)
	}
}
