package wikiapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// A second local Git writer creates a sibling of the detached editor commit.
// Previously both save tails re-read that sibling as the expected CAS value,
// overwrote it with the editor's tip, and removed the only editor worktree.
func TestSaveKeepsConcurrentLocalWikiHistory(t *testing.T) {
	requireGit(t)
	for _, mode := range []string{"no-sync", "push-tail"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			dir := newClone(t, initBare(t))
			s := New(dir)
			wt, err := s.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			writeWiki(t, wt, "Home.md", "base\n")
			baseResult, err := s.Save(ctx, "base", "origin", true)
			if err != nil {
				t.Fatal(err)
			}
			wt, err = s.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			writeWiki(t, wt, "Edit.md", "editor\n")
			r := gitx.New(dir)
			if _, err := r.CommitAll(ctx, wt, "editor"); err != nil {
				t.Fatal(err)
			}
			head, err := r.WorktreeHead(ctx, wt)
			if err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			tree := git("rev-parse", baseResult.CommitHash+"^{tree}")
			other := git("commit-tree", tree, "-p", baseResult.CommitHash, "-m", "other local writer")
			git("update-ref", refspace.WikiMainRef, other, baseResult.CommitHash)
			// Prove this is divergent history, rather than a harmless advanced base.
			ff, err := r.IsAncestor(ctx, other, head)
			if err != nil || ff {
				t.Fatalf("fixture is not divergent: ff=%v err=%v", ff, err)
			}
			if mode == "no-sync" {
				_, err = s.Save(ctx, "resume", "origin", true)
			} else {
				// The final push tail is also used after a successful remote merge.
				_, _, err = s.pushMergedOnce(ctx, r, wt, "origin")
			}
			var conflict *LocalAdvanceConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("expected local conflict, got %v", err)
			}
			if conflict.Current != other || conflict.Head != head || conflict.Worktree != wt {
				t.Fatalf("wrong recovery tips: %+v", conflict)
			}
			if got := git("rev-parse", refspace.WikiMainRef); got != other {
				t.Fatalf("other writer lost: %s != %s", got, other)
			}
			if got, err := r.WorktreeHead(ctx, wt); err != nil || got != head {
				t.Fatalf("editor lost: %s %v", got, err)
			}
			if cfg, err := r.ConfigGet(ctx, worktreeConfigKey); err != nil || cfg != wt {
				t.Fatalf("session lost: %q %v", cfg, err)
			}
			if _, err := os.Stat(wt); err != nil {
				t.Fatal(err)
			}
			if got := lsRemote(t, dir, "origin", refspace.WikiMainRef); got != "" {
				t.Fatalf("conflicting save pushed %s", got)
			}
			// An explicit ordinary Git merge repairs the session; saving again must
			// resume the existing commit, retain both histories, and clean up normally.
			cmd := exec.Command("git", "-C", wt, "merge", "--no-edit", refspace.WikiMainRef)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("repair: %v %s", err, out)
			}
			result, err := s.Save(ctx, "repaired", "origin", true)
			if err != nil {
				t.Fatal(err)
			}
			for _, tip := range []string{other, head} {
				if ok, err := r.IsAncestor(ctx, tip, result.CommitHash); err != nil || !ok {
					t.Fatalf("repair lost %s: %v", tip, err)
				}
			}
			assertNoWorktree(t, dir, wt)
		})
	}
}
