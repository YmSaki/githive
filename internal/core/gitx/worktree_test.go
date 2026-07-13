package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const wikiRef = "refs/projects/wiki/main"

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noCRLF disables git's autocrlf line-ending rewriting so worktree checkouts
// return byte-identical content on Windows and POSIX (the test repos have no
// .gitattributes of their own).
func noCRLF(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "config", "core.autocrlf", "false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config core.autocrlf: %v\n%s", err, out)
	}
}

func initBare(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	return origin
}

// TestWorktreeAddFirstSaveCommitRemove covers the first-ever wiki save: the
// wiki ref is absent, so WorktreeAdd checks out an empty tree; CommitAll then
// records the initial content; WorktreeRemove cleans the temp worktree fully.
func TestWorktreeAddFirstSaveCommitRemove(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	noCRLF(t, dir)
	r := New(dir)
	ctx := context.Background()

	wt, err := r.WorktreeAdd(ctx, wikiRef) // ref absent -> empty tree
	if err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}

	changed, err := r.HasChanges(ctx, wt)
	if err != nil || changed {
		t.Fatalf("empty worktree should have no changes: changed=%v err=%v", changed, err)
	}

	writeFile(t, wt, "Home.md", "# home\n")
	changed, err = r.HasChanges(ctx, wt)
	if err != nil || !changed {
		t.Fatalf("expected changes after write: changed=%v err=%v", changed, err)
	}

	head, err := r.CommitAll(ctx, wt, "add Home")
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if head == "" {
		t.Fatal("empty commit hash")
	}
	got, err := r.Show(ctx, head, "Home.md")
	if err != nil || string(got) != "# home\n" {
		t.Fatalf("Show after commit = %q err=%v", got, err)
	}

	if err := r.WorktreeRemove(ctx, wt); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir not cleaned: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(wt)); !os.IsNotExist(err) {
		t.Fatalf("temp parent not cleaned: %v", err)
	}
}

// TestWorktreeAddAtRef checks out an existing wiki ref so the worktree starts
// with the committed content.
func TestWorktreeAddAtRef(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	noCRLF(t, dir)
	r := New(dir)
	ctx := context.Background()

	wt, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, "Home.md", "base\n")
	head, err := r.CommitAll(ctx, wt, "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, wikiRef, head, ZeroOID); err != nil {
		t.Fatal(err)
	}
	if err := r.WorktreeRemove(ctx, wt); err != nil {
		t.Fatal(err)
	}

	wt2, err := r.WorktreeAdd(ctx, wikiRef) // ref now exists
	if err != nil {
		t.Fatal(err)
	}
	defer r.WorktreeRemove(ctx, wt2)
	b, err := os.ReadFile(filepath.Join(wt2, "Home.md"))
	if err != nil || string(b) != "base\n" {
		t.Fatalf("worktree content = %q err=%v", b, err)
	}
}

// TestMergeWikiRemoteClean merges a non-conflicting remote change (a different
// file) into the local worktree and reports no conflicts.
func TestMergeWikiRemoteClean(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	ctx := context.Background()

	base := seedWiki(t, origin, "Home.md", "base\n")

	// cloneB starts from base.
	cloneB := filepath.Join(t.TempDir(), "b")
	initRepo(t, cloneB)
	noCRLF(t, cloneB)
	rB := New(cloneB)
	if err := rB.Fetch(ctx, origin, "+"+wikiRef+":"+wikiRef); err != nil {
		t.Fatal(err)
	}
	wtB, err := rB.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	defer rB.WorktreeRemove(ctx, wtB)
	writeFile(t, wtB, "design/sync.md", "sync\n")
	if _, err := rB.CommitAll(ctx, wtB, "add sync page"); err != nil {
		t.Fatal(err)
	}

	// Meanwhile the remote gets a change to a DIFFERENT file.
	advanceWiki(t, origin, base, "ops/release.md", "release\n")

	conflicts, err := rB.MergeWikiRemote(ctx, wtB, origin, wikiRef, "refs/githive-remote/wiki/main")
	if err != nil {
		t.Fatalf("MergeWikiRemote: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected clean merge, got conflicts %v", conflicts)
	}
	// The merge pulled in the remote-only file.
	if _, err := os.Stat(filepath.Join(wtB, "ops", "release.md")); err != nil {
		t.Fatalf("remote file not merged in: %v", err)
	}
}

// TestMergeWikiRemoteConflictAndCleanup verifies MergeWikiRemote reports the
// conflicted paths without erroring, leaves MERGE_HEAD for a re-run, and that
// WorktreeRemove still cleans a conflicted worktree.
func TestMergeWikiRemoteConflictAndCleanup(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	ctx := context.Background()

	base := seedWiki(t, origin, "Home.md", "base\n")

	cloneB := filepath.Join(t.TempDir(), "b")
	initRepo(t, cloneB)
	noCRLF(t, cloneB)
	rB := New(cloneB)
	if err := rB.Fetch(ctx, origin, "+"+wikiRef+":"+wikiRef); err != nil {
		t.Fatal(err)
	}
	wtB, err := rB.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wtB, "Home.md", "BBB\n")
	if _, err := rB.CommitAll(ctx, wtB, "B edits Home"); err != nil {
		t.Fatal(err)
	}

	// Remote gets a conflicting change to the SAME file/region.
	advanceWiki(t, origin, base, "Home.md", "AAA\n")

	conflicts, err := rB.MergeWikiRemote(ctx, wtB, origin, wikiRef, "refs/githive-remote/wiki/main")
	if err != nil {
		t.Fatalf("MergeWikiRemote returned error on conflict (should not): %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "Home.md" {
		t.Fatalf("conflicts = %v, want [Home.md]", conflicts)
	}
	inMerge, err := rB.MergeInProgress(ctx, wtB)
	if err != nil || !inMerge {
		t.Fatalf("expected merge in progress: %v err=%v", inMerge, err)
	}

	// Cleanup must succeed even with a conflicted merge in progress.
	if err := rB.WorktreeRemove(ctx, wtB); err != nil {
		t.Fatalf("WorktreeRemove on conflicted worktree: %v", err)
	}
	if _, err := os.Stat(wtB); !os.IsNotExist(err) {
		t.Fatalf("conflicted worktree not cleaned: %v", err)
	}
}

// seedWiki creates the wiki with one file in a throwaway clone and pushes it to
// origin, returning the wiki commit hash now on origin.
func seedWiki(t *testing.T, origin, path, content string) string {
	t.Helper()
	ctx := context.Background()
	clone := filepath.Join(t.TempDir(), "seed")
	initRepo(t, clone)
	noCRLF(t, clone)
	r := New(clone)
	wt, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, path, content)
	head, err := r.CommitAll(ctx, wt, "seed wiki")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, wikiRef, head, ZeroOID); err != nil {
		t.Fatal(err)
	}
	if results, err := r.Push(ctx, origin, wikiRef+":"+wikiRef); err != nil || !results[0].OK {
		t.Fatalf("seed push: %v %+v", err, results)
	}
	if err := r.WorktreeRemove(ctx, wt); err != nil {
		t.Fatal(err)
	}
	return head
}

// advanceWiki commits a change on top of parent in a throwaway clone and pushes
// it to origin, simulating another writer advancing the remote wiki.
func advanceWiki(t *testing.T, origin, parent, path, content string) {
	t.Helper()
	ctx := context.Background()
	clone := filepath.Join(t.TempDir(), "adv")
	initRepo(t, clone)
	noCRLF(t, clone)
	r := New(clone)
	if err := r.Fetch(ctx, origin, "+"+wikiRef+":"+wikiRef); err != nil {
		t.Fatal(err)
	}
	wt, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, path, content)
	head, err := r.CommitAll(ctx, wt, "advance wiki")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, wikiRef, head, parent); err != nil {
		t.Fatal(err)
	}
	if results, err := r.Push(ctx, origin, wikiRef+":"+wikiRef); err != nil || !results[0].OK {
		t.Fatalf("advance push: %v %+v", err, results)
	}
	if err := r.WorktreeRemove(ctx, wt); err != nil {
		t.Fatal(err)
	}
}
