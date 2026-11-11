package syncapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/app/wikiapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

const wikiRef = refspace.WikiMainRef

// seedWikiCommit writes path with content in dir's working tree, commits it,
// and points refs/projects/wiki/main at the result - the raw-git equivalent
// of `githive wiki save`, without going through the worktree flow.
func seedWikiCommit(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "add", path},
		{"-C", dir, "commit", "-q", "-m", msg},
		{"-C", dir, "update-ref", wikiRef, "HEAD"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestSyncWikiFastForwardsBothWays is ADR-0017's core promise: a wiki page
// another clone published becomes visible through `githive sync`, which was
// impossible before - only `wiki save` ever touched the remote.
func TestSyncWikiFastForwardsBothWays(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	seedWikiCommit(t, dirA, "Home.md", "# home\n", "add Home")

	resA, err := Sync(ctx, dirA, "origin", []string{wikiRef}, 5)
	if err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if len(resA) != 1 || resA[0].Action != ActionFastForwardOut {
		t.Fatalf("expected A to push the wiki, got %+v", resA)
	}

	resB, err := Sync(ctx, dirB, "origin", []string{wikiRef}, 5)
	if err != nil {
		t.Fatalf("B sync: %v", err)
	}
	if len(resB) != 1 || resB[0].Action != ActionFastForwardIn {
		t.Fatalf("expected B to fast-forward the wiki in, got %+v", resB)
	}

	body, err := gitx.New(dirB).Show(ctx, wikiRef, "Home.md")
	if err != nil {
		t.Fatalf("B should now have the page: %v", err)
	}
	if string(body) != "# home\n" {
		t.Errorf("unexpected page content: %q", body)
	}
}

// TestSyncWikiDivergedWarnsInsteadOfMerging: sync must not attempt the 3-way
// merge that `wiki save` owns, must leave the ref alone, and must not fail
// the whole sync over it (ADR-0017).
func TestSyncWikiDivergedWarnsInsteadOfMerging(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// A publishes, B writes its own unrelated wiki history.
	seedWikiCommit(t, dirA, "Home.md", "# from a\n", "a home")
	if _, err := Sync(ctx, dirA, "origin", []string{wikiRef}, 5); err != nil {
		t.Fatal(err)
	}
	seedWikiCommit(t, dirB, "Home.md", "# from b\n", "b home")
	before := refOID(t, dirB, wikiRef)

	results, err := Sync(ctx, dirB, "origin", []string{wikiRef}, 5)
	if err != nil {
		t.Fatalf("a diverged wiki must not fail the sync: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionWikiDiverged {
		t.Fatalf("expected %s, got %+v", ActionWikiDiverged, results)
	}
	if results[0].Warning == "" {
		t.Error("expected a warning pointing at `githive wiki edit` / `wiki save`")
	}
	if after := refOID(t, dirB, wikiRef); after != before {
		t.Errorf("sync moved the diverged wiki ref: %s -> %s", before, after)
	}
}

// TestSyncWikiSkippedWhileEditing: with an edit worktree checked out, the ref
// must not move under it, even for a fast-forward.
func TestSyncWikiSkippedWhileEditing(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	seedWikiCommit(t, dirA, "Home.md", "# from a\n", "a home")
	if _, err := Sync(ctx, dirA, "origin", []string{wikiRef}, 5); err != nil {
		t.Fatal(err)
	}

	// B starts editing; its wiki ref does not exist yet, so without the
	// guard this sync would fast-forward it in underneath the worktree.
	worktree, err := wikiapp.New(dirB).Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	before := refOID(t, dirB, wikiRef)

	results, err := Sync(ctx, dirB, "origin", []string{wikiRef}, 5)
	if err != nil {
		t.Fatalf("an in-progress edit must not fail the sync: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionWikiSkippedEditing {
		t.Fatalf("expected %s, got %+v", ActionWikiSkippedEditing, results)
	}
	if results[0].Warning == "" {
		t.Error("expected a warning naming the in-progress worktree")
	}
	if after := refOID(t, dirB, wikiRef); after != before {
		t.Errorf("sync moved the wiki ref during an edit: %s -> %s", before, after)
	}
	_ = worktree
}

// Two independently initialized project configs require an explicit choice.
// A two-parent resolution preserves both histories and pushes without force.
func TestSyncMetaConfigConflictCanBeResolvedWithoutLosingHistory(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	if _, err := initapp.Init(ctx, dirA, "origin", "proj-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := initapp.Init(ctx, dirB, "origin", "proj-b"); err != nil {
		t.Fatal(err)
	}

	metaRef := refspace.MetaConfigRef
	if _, err := Sync(ctx, dirA, "origin", []string{metaRef}, 5); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	localBefore, remoteBefore := refOID(t, dirB, metaRef), refOID(t, dirA, metaRef)
	_, err := Sync(ctx, dirB, "origin", []string{metaRef}, 5)
	if !errors.Is(err, ErrMetaConfigConflict) {
		t.Fatalf("expected config conflict: %v", err)
	}
	if refOID(t, dirB, metaRef) != localBefore {
		t.Fatal("conflict changed B's config ref")
	}

	// Model the documented operator decision: retain A's reviewed config,
	// with both A and B as parents. No ref is reset and no push is forced.
	body, err := gitx.New(dirB).Show(ctx, remoteBefore, "config.json")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dirB)
	if err != nil {
		t.Fatal(err)
	}
	merge, err := chain.AppendMerge(repo, [2]plumbing.Hash{plumbing.NewHash(localBefore), plumbing.NewHash(remoteBefore)}, map[string][]byte{"config.json": body}, chain.Signature{Name: "B", Email: "b@example.com", When: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dirB, plumbing.ReferenceName(metaRef), merge, localBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirB, "origin", []string{metaRef}, 5); err != nil {
		t.Fatalf("push resolution: %v", err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{metaRef}, 5); err != nil {
		t.Fatalf("fetch resolution: %v", err)
	}
	if a, b := refOID(t, dirA, metaRef), refOID(t, dirB, metaRef); a != b || a != merge.String() {
		t.Fatalf("resolution did not converge: %s %s", a, b)
	}
	for _, old := range []string{localBefore, remoteBefore} {
		retained, err := gitx.New(dirB).IsAncestor(ctx, old, merge.String())
		if err != nil {
			t.Fatal(err)
		}
		if !retained {
			t.Fatalf("resolution lost history %s", old)
		}
	}
}

// TestConfigSchemaVersion parses the minimum required version used by merge.
func TestConfigSchemaVersion(t *testing.T) {
	older := []byte(`{"project":"p","schema_version":1}` + "\n")
	newer := []byte(`{"project":"p","schema_version":2}` + "\n")

	oldV, err := configSchemaVersion(older)
	if err != nil {
		t.Fatal(err)
	}
	newV, err := configSchemaVersion(newer)
	if err != nil {
		t.Fatal(err)
	}
	if oldV != 1 || newV != 2 {
		t.Fatalf("configSchemaVersion read %d and %d", oldV, newV)
	}

	missing, err := configSchemaVersion([]byte(`{"project":"p"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Errorf("a config.json without schema_version should read as 0, got %d", missing)
	}
}

// TestSupportedFeaturesCoversEveryRefKind guards the list sync, status and
// DiscoverRefs all share: a feature missing from it is silently never synced.
func TestSupportedFeaturesCoversEveryRefKind(t *testing.T) {
	for _, f := range []refspace.Feature{
		refspace.FeatureIssue, refspace.FeatureTask, refspace.FeatureChat,
		refspace.FeatureNotify, refspace.FeatureUsers,
		refspace.FeatureMeta, refspace.FeatureWiki,
	} {
		if !SupportedFeatures[f] {
			t.Errorf("feature %q is not in SupportedFeatures, so sync and status ignore it", f)
		}
	}
}
