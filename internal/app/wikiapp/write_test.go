package wikiapp

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wikifs"
)

func initBare(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	return origin
}

// newClone makes a fresh non-bare repo configured to fetch origin's projects
// refs into the remote-tracking namespace, with autocrlf disabled so content
// round-trips byte-identically on Windows.
func newClone(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "--quiet")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "core.autocrlf", "false")
	run("remote", "add", "origin", origin)
	return dir
}

func writeWiki(t *testing.T, wt, rel, content string) {
	t.Helper()
	full := filepath.Join(wt, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEditSaveRoundTrip is the happy path: first-ever wiki, edit, save pushes
// to the bare origin and cleans the worktree.
func TestEditSaveRoundTrip(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	writeWiki(t, wt, "Home.md", "# home\n")

	res, err := s.Save(ctx, "add Home", "origin", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !res.Pushed || res.CommitHash == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	// Origin has the wiki now.
	r := gitx.New(dir)
	remoteOID := lsRemote(t, dir, origin, refspace.WikiMainRef)
	localOID, _ := r.RevParse(ctx, refspace.WikiMainRef)
	if remoteOID == "" || remoteOID != localOID {
		t.Fatalf("origin=%q local=%q", remoteOID, localOID)
	}
	// Worktree removed and config cleared.
	assertNoWorktree(t, dir, wt)
	if cfg, _ := r.ConfigGet(ctx, worktreeConfigKey); cfg != "" {
		t.Fatalf("worktree config not cleared: %q", cfg)
	}
	// Content readable via the read side.
	got, err := s.Show(ctx, "Home.md")
	if err != nil || string(got) != "# home\n" {
		t.Fatalf("Show = %q err=%v", got, err)
	}
}

// TestSaveNoSync commits and advances the local ref only, no push.
func TestSaveNoSync(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "hi\n")
	res, err := s.Save(ctx, "add", "origin", true) // noSync
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed {
		t.Fatal("expected Pushed=false with --no-sync")
	}
	if lsRemote(t, dir, origin, refspace.WikiMainRef) != "" {
		t.Fatal("no-sync should not have pushed to origin")
	}
	local, _ := gitx.New(dir).RevParse(ctx, refspace.WikiMainRef)
	if local != res.CommitHash {
		t.Fatalf("local ref %q != committed %q", local, res.CommitHash)
	}
	assertNoWorktree(t, dir, wt)
}

// TestSaveRejectsBadFilename: a Windows-reserved name is a validation failure
// (→ exit 4), returned before any commit; the worktree is retained so the human
// can fix it, and a corrected re-save then succeeds and cleans up.
func TestSaveRejectsBadFilename(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "CON.md", "bad\n") // Windows reserved device name

	_, err = s.Save(ctx, "bad", "origin", false)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if len(ve.Violations) == 0 || ve.Violations[0].Code != "reserved_name" {
		t.Fatalf("violations = %+v", ve.Violations)
	}
	// Nothing was committed.
	if oid, _ := gitx.New(dir).RevParse(ctx, refspace.WikiMainRef); oid != "" {
		t.Fatalf("wiki ref advanced despite validation failure: %q", oid)
	}
	// Worktree retained for the fix.
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree should be retained after validation reject: %v", statErr)
	}

	// Fix and re-save.
	if err := os.Remove(filepath.Join(wt, "CON.md")); err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Ok.md", "good\n")
	res, err := s.Save(ctx, "fixed", "origin", false)
	if err != nil {
		t.Fatalf("re-save after fix: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected push after fix")
	}
	assertNoWorktree(t, dir, wt)
}

// TestSaveRejectsOversizeAsset: a file under _assets/ over 1 MiB is a
// validation failure.
func TestSaveRejectsOversizeAsset(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	ctx := context.Background()

	wt, err := s.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "ok\n")
	big := make([]byte, wikifs.MaxAssetBytes+1)
	writeWiki(t, wt, "_assets/big.bin", string(big))

	_, err = s.Save(ctx, "big", "origin", false)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	found := false
	for _, v := range ve.Violations {
		if v.Code == "oversize_asset" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected oversize_asset violation, got %+v", ve.Violations)
	}
}

// TestConflictAndRerun: two clones edit the same page from a common base; the
// second save reports the conflict without pushing (→ exit 3), then a re-save
// after the human resolves the markers completes and pushes.
func TestConflictAndRerun(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	ctx := context.Background()

	// Seed the wiki from clone A.
	dirA := newClone(t, origin)
	sA := New(dirA)
	wtA0, err := sA.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtA0, "Home.md", "base\n")
	if _, err := sA.Save(ctx, "seed", "origin", false); err != nil {
		t.Fatal(err)
	}

	// Clone B fetches base and starts an edit.
	dirB := newClone(t, origin)
	sB := New(dirB)
	rB := gitx.New(dirB)
	if err := rB.Fetch(ctx, origin, "+"+refspace.WikiMainRef+":"+refspace.WikiMainRef); err != nil {
		t.Fatal(err)
	}
	wtB, err := sB.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtB, "Home.md", "from-B\n")

	// Clone A makes a conflicting change and pushes first.
	wtA1, err := sA.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtA1, "Home.md", "from-A\n")
	if _, err := sA.Save(ctx, "A change", "origin", false); err != nil {
		t.Fatal(err)
	}

	// Clone B saves: commit + merge origin(A) → conflict, no push.
	_, err = sB.Save(ctx, "B change", "origin", false)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want *ConflictError, got %v", err)
	}
	if len(ce.Paths) != 1 || ce.Paths[0] != "Home.md" {
		t.Fatalf("conflict paths = %v", ce.Paths)
	}
	// B did not push its unresolved state.
	originAfterConflict := lsRemote(t, dirB, origin, refspace.WikiMainRef)
	// Worktree retained with markers.
	if _, statErr := os.Stat(wtB); statErr != nil {
		t.Fatalf("conflict worktree should be retained: %v", statErr)
	}
	marked, _ := os.ReadFile(filepath.Join(wtB, "Home.md"))
	if !strings.Contains(string(marked), "<<<<<<<") {
		t.Fatalf("expected conflict markers in worktree, got %q", marked)
	}

	// Human resolves the markers and re-runs save.
	writeWiki(t, wtB, "Home.md", "resolved\n")
	res, err := sB.Save(ctx, "resolve", "origin", false)
	if err != nil {
		t.Fatalf("re-run save after resolution: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected push after resolution")
	}
	// Origin advanced past the pre-resolution state.
	originFinal := lsRemote(t, dirB, origin, refspace.WikiMainRef)
	if originFinal == originAfterConflict {
		t.Fatal("origin did not advance after resolution push")
	}
	assertNoWorktree(t, dirB, wtB)

	// The merge commit contains the resolved content.
	got, err := sB.Show(ctx, "Home.md")
	if err != nil || string(got) != "resolved\n" {
		t.Fatalf("resolved Show = %q err=%v", got, err)
	}
}

// TestPushRaceRetry: after B's clean local merge but before its push, the
// remote advances (a non-conflicting change). The push is rejected, and Save's
// retry loop re-fetches/re-merges/re-pushes to success.
func TestPushRaceRetry(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	ctx := context.Background()

	// Seed the wiki.
	dirA := newClone(t, origin)
	sA := New(dirA)
	wtA, err := sA.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtA, "Home.md", "base\n")
	if _, err := sA.Save(ctx, "seed", "origin", false); err != nil {
		t.Fatal(err)
	}

	// Clone B edits a different file.
	dirB := newClone(t, origin)
	sB := New(dirB)
	rB := gitx.New(dirB)
	if err := rB.Fetch(ctx, origin, "+"+refspace.WikiMainRef+":"+refspace.WikiMainRef); err != nil {
		t.Fatal(err)
	}
	wtB, err := sB.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtB, "design/sync.md", "sync\n")

	// Seam: right before B's first push, a third writer advances origin with a
	// non-conflicting change, forcing a push race that the retry must absorb.
	raced := false
	sB.afterMerge = func() {
		raced = true
		dirC := newClone(t, origin)
		sC := New(dirC)
		rC := gitx.New(dirC)
		if err := rC.Fetch(ctx, origin, "+"+refspace.WikiMainRef+":"+refspace.WikiMainRef); err != nil {
			t.Fatal(err)
		}
		wtC, err := sC.Edit(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		writeWiki(t, wtC, "ops/release.md", "release\n")
		if _, err := sC.Save(ctx, "C change", "origin", false); err != nil {
			t.Fatal(err)
		}
	}

	res, err := sB.Save(ctx, "B change", "origin", false)
	if err != nil {
		t.Fatalf("Save under push race: %v", err)
	}
	if !raced {
		t.Fatal("test seam did not fire")
	}
	if !res.Pushed {
		t.Fatal("expected eventual push")
	}
	// Final origin has all three files.
	if err := rB.Fetch(ctx, origin, "+"+refspace.WikiMainRef+":"+refspace.WikiMainRef); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Home.md", "design/sync.md", "ops/release.md"} {
		if _, err := sB.Show(ctx, p); err != nil {
			t.Errorf("expected %s in merged wiki: %v", p, err)
		}
	}
	assertNoWorktree(t, dirB, wtB)
}

// TestSaveWithoutEdit errors cleanly when no worktree is recorded.
func TestSaveWithoutEdit(t *testing.T) {
	requireGit(t)
	origin := initBare(t)
	dir := newClone(t, origin)
	s := New(dir)
	if _, err := s.Save(context.Background(), "x", "origin", false); !errors.Is(err, ErrNoEditInProgress) {
		t.Fatalf("want ErrNoEditInProgress, got %v", err)
	}
}

// TestWikiappNoEventSourcingImports proves structurally that the wiki write
// path never enters internal/core/materialize (or the event-union / entitychain
// machinery): wiki is the one feature that is not event-sourced.
func TestWikiappNoEventSourcingImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"internal/core/materialize",
		"internal/core/merge",
		"internal/app/entitychain",
		"internal/core/event",
	}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if strings.Contains(p, bad) {
						t.Errorf("%s imports %s: wiki must not use event sourcing / materialize", name, p)
					}
				}
			}
		}
	}
}

func lsRemote(t *testing.T, dir, remote, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "ls-remote", remote, ref).Output()
	if err != nil {
		t.Fatalf("ls-remote: %v", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return ""
	}
	return strings.Fields(line)[0]
}

func assertNoWorktree(t *testing.T, dir, wt string) {
	t.Helper()
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree not cleaned: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if strings.Contains(string(out), filepath.ToSlash(wt)) || strings.Contains(string(out), wt) {
		t.Fatalf("leaked worktree registration:\n%s", out)
	}
}
