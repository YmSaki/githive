package wikiapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestSavePreservesManualWorktreeParentAndSiblings(t *testing.T) {
	requireGit(t)
	for _, relative := range []bool{false, true} {
		t.Run(fmt.Sprint("relative=", relative), func(t *testing.T) {
			ctx := context.Background()
			dir := newClone(t, initBare(t))
			s := New(dir)
			r := gitx.New(dir)
			wt, err := s.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			writeWiki(t, wt, "Home.md", "seed\n")
			if _, err := s.Save(ctx, "seed", "origin", true); err != nil {
				t.Fatal(err)
			}
			manual := filepath.Join(filepath.Dir(dir), "mywiki")
			if out, err := exec.Command("git", "-C", dir, "worktree", "add", "--detach", manual, refspace.WikiMainRef).CombinedOutput(); err != nil {
				t.Fatalf("manual worktree: %v %s", err, out)
			}
			configured := manual
			if relative {
				configured, err = filepath.Rel(dir, manual)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ConfigSet(ctx, worktreeConfigKey, configured); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(filepath.Dir(dir), "unrelated.txt")
			if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			writeWiki(t, manual, "Home.md", "updated\n")
			if _, err := s.Save(ctx, "manual update", "origin", true); err != nil {
				t.Fatal(err)
			}
			for _, keep := range []string{dir, manual, sentinel} {
				if _, err := os.Stat(keep); err != nil {
					t.Fatalf("save deleted %s: %v", keep, err)
				}
			}
			if got, err := s.Show(ctx, "Home.md"); err != nil || string(got) != "updated\n" {
				t.Fatalf("saved wiki=%q err=%v", got, err)
			}
			if got, err := r.ConfigGet(ctx, worktreeConfigKey); err != nil || got != "" {
				t.Fatalf("completed edit config=%q err=%v", got, err)
			}
		})
	}
}
