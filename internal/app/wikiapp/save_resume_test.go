package wikiapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// TestSaveOfflineKeepsCommitOnLocalRef is #119's offline case: with the
// remote unreachable, save must not leave the commit only on the
// worktree's detached HEAD. It advances the local wiki ref, removes the
// worktree, and reports the sync failure as a warning; a later push from
// the local ref publishes it.
func TestSaveOfflineKeepsCommitOnLocalRef(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()
	r := gitx.New(dir)

	// Point origin somewhere unreachable for the save.
	offline := filepath.Join(t.TempDir(), "gone.git")
	if out, err := exec.Command("git", "-C", dir, "remote", "set-url", "origin", offline).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "offline edit\n")
	res, err := s.Save(ctx, "offline", "origin", false)
	if err != nil {
		t.Fatalf("offline save must succeed locally, got %v", err)
	}
	if res.Pushed || res.Warning == "" {
		t.Fatalf("want a not-pushed result with a warning, got %+v", res)
	}
	local, _ := r.RevParse(ctx, refspace.WikiMainRef)
	if local == "" || local != res.CommitHash {
		t.Fatalf("local wiki ref = %q, want %q", local, res.CommitHash)
	}
	assertNoWorktree(t, dir, wt)
	if got, err := s.Show(ctx, "Home.md"); err != nil || string(got) != "offline edit\n" {
		t.Fatalf("Show = %q, %v", got, err)
	}

	// Back online: the commit is on the local ref, so a plain push of it
	// (what `githive sync` does for a local-ahead wiki) publishes it.
	if out, err := exec.Command("git", "-C", dir, "remote", "set-url", "origin", origin).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if results, err := r.Push(ctx, "origin", refspace.WikiMainRef+":"+refspace.WikiMainRef); err != nil || !pushOK(results) {
		t.Fatalf("push: %+v %v", results, err)
	}
	if got := lsRemote(t, dir, origin, refspace.WikiMainRef); got != local {
		t.Fatalf("origin wiki = %q, want %q", got, local)
	}
}

// TestSaveResumesUnpublishedWorktreeCommit: a worktree whose edit was
// already committed by an interrupted save must be resumed (merge + push),
// not rejected as "no changes to save".
func TestSaveResumesUnpublishedWorktreeCommit(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "committed but not saved\n")
	if _, err := gitx.New(dir).CommitAll(ctx, wt, "interrupted"); err != nil {
		t.Fatal(err)
	}

	res, err := s.Save(ctx, "resume", "origin", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("want pushed, got %+v", res)
	}
	if got := lsRemote(t, dir, origin, refspace.WikiMainRef); got != res.CommitHash {
		t.Fatalf("origin wiki = %q, want %q", got, res.CommitHash)
	}
	assertNoWorktree(t, dir, wt)
}

// TestSaveAfterRejectedPushResumes is #119's pre-receive case: every push
// is refused, so save fails with the commit on the local ref. Re-running
// save, as the error says, must retry the push instead of reporting "no
// changes to save"; once the hook is gone it publishes.
func TestSaveAfterRejectedPushResumes(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho denied >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "x\n")
	_, err = s.Save(ctx, "first", "origin", false)
	if !errors.Is(err, ErrPushRaceExhausted) {
		t.Fatalf("want ErrPushRaceExhausted, got %v", err)
	}
	local, _ := gitx.New(dir).RevParse(ctx, refspace.WikiMainRef)
	if local == "" {
		t.Fatal("the commit must be saved on the local wiki ref")
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	res, err := s.Save(ctx, "retry", "origin", false)
	if err != nil {
		t.Fatalf("re-run save: %v", err)
	}
	if !res.Pushed || lsRemote(t, dir, origin, refspace.WikiMainRef) != local {
		t.Fatalf("want the saved commit pushed, got %+v", res)
	}
	assertNoWorktree(t, dir, wt)
}

// TestSaveNoChangesStillNothingToSave: an untouched edit is still "no
// changes", both for a fresh wiki and for one already in sync.
func TestSaveNoChangesStillNothingToSave(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	if _, err := s.Edit(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "nothing", "origin", false); !errors.Is(err, ErrNothingToSave) {
		t.Fatalf("fresh wiki: want ErrNothingToSave, got %v", err)
	}
	wt, _ := gitx.New(dir).ConfigGet(ctx, worktreeConfigKey)
	writeWiki(t, wt, "Home.md", "x\n")
	if _, err := s.Save(ctx, "add", "origin", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Edit(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "nothing", "origin", false); !errors.Is(err, ErrNothingToSave) {
		t.Fatalf("synced wiki: want ErrNothingToSave, got %v", err)
	}
}
