package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeRemovePreservesUnmanagedPaths(t *testing.T) {
	requireGit(t)
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repo)
	r := New(repo)
	ctx := context.Background()
	managed, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WorktreeRemove(ctx, managed)
	head, err := r.WorktreeHead(ctx, managed)
	if err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(parent, "mywiki")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--detach", manual, head).CombinedOutput(); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	sentinel := filepath.Join(parent, "unrelated.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manual, "../mywiki", repo, "."} {
		if err := r.WorktreeRemove(ctx, path); err != nil {
			t.Fatal(err)
		}
		for _, keep := range []string{repo, manual, sentinel} {
			if _, err := os.Stat(keep); err != nil {
				t.Fatalf("unmanaged path %s deleted while removing %s: %v", keep, path, err)
			}
		}
	}
}

func TestWorktreeRemoveRequiresMatchingRepositoryOwner(t *testing.T) {
	requireGit(t)
	a, b := t.TempDir(), t.TempDir()
	initRepo(t, a)
	initRepo(t, b)
	ctx := context.Background()
	r := New(a)
	wt, err := r.WorktreeAdd(ctx, wikiRef)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WorktreeRemove(ctx, wt)
	if err := New(b).WorktreeRemove(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("foreign repository deleted managed worktree: %v", err)
	}
	sibling := filepath.Join(filepath.Dir(wt), "unrelated.txt")
	if err := os.WriteFile(sibling, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.WorktreeRemove(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("owned worktree not removed: %v", err)
	}
	if b, err := os.ReadFile(sibling); err != nil || string(b) != "keep" {
		t.Fatalf("sibling lost: %q %v", b, err)
	}
	t.Cleanup(func() { os.Remove(sibling); os.Remove(filepath.Dir(wt)) })
}

func TestWorktreeRemovePreservesUnmarkedAndSymlinkPaths(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	initRepo(t, repo)
	ctx := context.Background()
	r := New(repo)
	root, err := os.MkdirTemp("", "githive-wiki-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	fake := filepath.Join(root, "worktree")
	if err := os.Mkdir(fake, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.WorktreeRemove(ctx, fake); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fake); err != nil {
		t.Fatalf("unmarked path deleted: %v", err)
	}
	target := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := r.WorktreeRemove(ctx, filepath.Join(link, "worktree")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target deleted: %v", err)
	}
}
