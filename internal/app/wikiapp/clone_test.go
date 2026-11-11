package wikiapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestEditSaveFreshCloneUsesRemoteWiki(t *testing.T) {
	ctx := context.Background()
	origin := initBare(t)
	a := newClone(t, origin)
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// The source branch is deliberately not master/main: wiki selection must
	// use the explicit projects ref rather than the clone's HEAD branch.
	run(a, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(a, "README.md"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(a, "add", "README.md")
	run(a, "commit", "-m", "source")
	run(a, "push", "origin", "develop")
	run(origin, "symbolic-ref", "HEAD", "refs/heads/develop")
	wtA, err := New(a).Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wtA, "Home.md", "remote page\n")
	initial, err := New(a).Save(ctx, "initial wiki", "origin", false)
	if err != nil || !initial.Pushed {
		t.Fatalf("initial save: %+v %v", initial, err)
	}
	for _, remote := range []string{"origin", "upstream"} {
		t.Run(remote, func(t *testing.T) {
			b := filepath.Join(t.TempDir(), "clone")
			if out, err := exec.Command("git", "clone", origin, b).CombinedOutput(); err != nil {
				t.Fatalf("clone: %v\n%s", err, out)
			}
			if remote != "origin" {
				run(b, "remote", "rename", "origin", remote)
			}
			run(b, "config", "user.name", "Different Author")
			run(b, "config", "user.email", "other@example.com")
			r := gitx.New(b)
			if oid, err := r.RevParse(ctx, refspace.WikiMainRef); err != nil || oid != "" {
				t.Fatalf("plain clone unexpectedly has local wiki ref: %q %v", oid, err)
			}
			wtB, err := New(b).EditWithRemote(ctx, false, remote, false)
			if err != nil {
				t.Fatal(err)
			}
			defer r.WorktreeRemove(ctx, wtB)
			if got, err := os.ReadFile(filepath.Join(wtB, "Home.md")); err != nil || string(got) != "remote page\n" {
				t.Fatalf("fresh clone's wiki edit lost remote page: %q %v", got, err)
			}
			writeWiki(t, wtB, "New.md", "new page "+remote+"\n")
			saved, err := New(b).Save(ctx, "add page", remote, false)
			if err != nil || !saved.Pushed {
				t.Fatalf("fresh clone first save: %+v %v", saved, err)
			}
			if ancestor, err := r.IsAncestor(ctx, initial.CommitHash, saved.CommitHash); err != nil || !ancestor {
				t.Fatalf("remote wiki history was not preserved: %v %v", ancestor, err)
			}
			if got, err := New(b).Show(ctx, "Home.md"); err != nil || string(got) != "remote page\n" {
				t.Fatalf("saved wiki lost original page: %q %v", got, err)
			}
		})
	}
}

func TestEditWithRemoteNoSyncUsesCachedWiki(t *testing.T) {
	ctx := context.Background()
	origin := initBare(t)
	a := newClone(t, origin)
	wt, err := New(a).Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	writeWiki(t, wt, "Home.md", "cached page\n")
	if _, err := New(a).Save(ctx, "seed", "origin", false); err != nil {
		t.Fatal(err)
	}
	b := newClone(t, origin)
	r := gitx.New(b)
	if err := r.Fetch(ctx, "origin", "+"+refspace.WikiMainRef+":refs/githive-remote/wiki/main"); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "git.trace")
	t.Setenv("GIT_TRACE", trace)
	wt, err = New(b).EditWithRemote(ctx, false, "origin", true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WorktreeRemove(ctx, wt)
	if got, err := os.ReadFile(filepath.Join(wt, "Home.md")); err != nil || string(got) != "cached page\n" {
		t.Fatalf("cached wiki missing: %q %v", got, err)
	}
	commands, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "git fetch ") {
		t.Fatalf("no-sync edit contacted remote:\n%s", commands)
	}
}
