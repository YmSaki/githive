package gitx

import (
	"context"
	"os/exec"
	"testing"
)

func TestWorktreeAddUsesCachedWikiWithoutLocalRef(t *testing.T) {
	ctx := context.Background()
	origin := initBare(t)
	seed := seedWiki(t, origin, "Home.md", "remote\n")
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	if err := r.Fetch(ctx, origin, "+"+wikiRef+":refs/githive-remote/wiki/main"); err != nil {
		t.Fatal(err)
	}
	wt, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WorktreeRemove(ctx, wt)
	if got, err := r.WorktreeHead(ctx, wt); err != nil || got != seed {
		t.Fatalf("cached wiki base = %q, want %q: %v", got, seed, err)
	}
}

func TestWorktreeAddIndependentWikiRootsAreIdentical(t *testing.T) {
	ctx := context.Background()
	var roots []string
	for _, email := range []string{"alice@example.com", "bob@example.com"} {
		dir := t.TempDir()
		initRepo(t, dir)
		for _, args := range [][]string{
			{"config", "user.email", email},
			{"config", "commit.gpgsign", "true"},
			{"config", "gpg.program", "does-not-exist"},
		} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		r := New(dir)
		wt, err := r.WorktreeAdd(ctx, wikiRef)
		if err != nil {
			t.Fatal(err)
		}
		defer r.WorktreeRemove(ctx, wt)
		root, err := r.WorktreeHead(ctx, wt)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
	}
	if roots[0] != roots[1] {
		t.Fatalf("independent wiki roots differ: %v", roots)
	}
}
