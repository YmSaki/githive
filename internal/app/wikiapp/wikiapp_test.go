package wikiapp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "init", "--quiet")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test")
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitWikiFile writes path with content, commits it, and advances
// refs/projects/wiki/main to the new HEAD.
func commitWikiFile(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", path)
	run(t, dir, "commit", "-m", msg)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := string(bytes.TrimSpace(out))
	// Advance the wiki ref (create on first call, fast-forward after).
	old := gitx.ZeroOID
	if cur, err := gitx.New(dir).RevParse(context.Background(), refspace.WikiMainRef); err == nil && cur != "" {
		old = cur
	}
	if err := gitx.New(dir).UpdateRef(context.Background(), refspace.WikiMainRef, head, old); err != nil {
		t.Fatal(err)
	}
}

func TestShowAndLogEmptyWiki(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	s := New(dir)
	ctx := context.Background()

	// No wiki ref yet: Log is empty (not an error), Show is not-found.
	entries, err := s.Log(ctx, "")
	if err != nil {
		t.Fatalf("Log on empty wiki: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty log, got %+v", entries)
	}
	if _, err := s.Show(ctx, "Home.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Show on empty wiki: want ErrNotFound, got %v", err)
	}
}

func TestShow(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	s := New(dir)
	ctx := context.Background()

	commitWikiFile(t, dir, "Home.md", "# home\n", "add Home")

	got, err := s.Show(ctx, "Home.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# home\n" {
		t.Errorf("Show = %q", got)
	}
	if _, err := s.Show(ctx, "Missing.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Show missing: want ErrNotFound, got %v", err)
	}
}

func TestLog(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	s := New(dir)
	ctx := context.Background()

	commitWikiFile(t, dir, "Home.md", "one\n", "first")
	commitWikiFile(t, dir, "design/sync.md", "sync\n", "second")

	entries, err := s.Log(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0]["subject"] != "second" || entries[1]["subject"] != "first" {
		t.Errorf("wrong order: %+v", entries)
	}
	for _, k := range []string{"hash", "author", "date", "subject"} {
		if v, ok := entries[0][k].(string); !ok || v == "" {
			t.Errorf("entry missing/empty key %q: %+v", k, entries[0])
		}
	}
	if entries[0]["author"] != "test@example.com" {
		t.Errorf("author = %v", entries[0]["author"])
	}

	only, err := s.Log(ctx, "Home.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0]["subject"] != "first" {
		t.Fatalf("path-filtered log = %+v", only)
	}
}
