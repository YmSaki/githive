package wikiapp

import (
	"context"
	"errors"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestSaveUnchangedEditReconcilesDivergedWiki(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "clean"
		if conflict {
			name = "conflict"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			origin := initBare(t)
			a := newClone(t, origin)
			sa := New(a)
			wt, err := sa.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			writeWiki(t, wt, "Home.md", "common base\n")
			if _, err := sa.Save(ctx, "base", "origin", false); err != nil {
				t.Fatal(err)
			}
			b := newClone(t, origin)
			sb := New(b)
			wtB, err := sb.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			localPath, remotePath := "Local.md", "Remote.md"
			if conflict {
				localPath, remotePath = "Home.md", "Home.md"
			}
			writeWiki(t, wtB, localPath, "local edit\n")
			local, err := sb.Save(ctx, "local offline", "origin", true)
			if err != nil {
				t.Fatal(err)
			}
			wt, err = sa.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			writeWiki(t, wt, remotePath, "remote edit\n")
			remote, err := sa.Save(ctx, "remote online", "origin", false)
			if err != nil || !remote.Pushed {
				t.Fatalf("remote save: %+v %v", remote, err)
			}
			r := gitx.New(b)
			if err := r.Fetch(ctx, "origin", "+"+refspace.WikiMainRef+":refs/githive-remote/wiki/main"); err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{local.CommitHash, remote.CommitHash}, {remote.CommitHash, local.CommitHash}} {
				if ancestor, err := r.IsAncestor(ctx, pair[0], pair[1]); err != nil || ancestor {
					t.Fatalf("test must start with diverged history: ancestor=%v err=%v", ancestor, err)
				}
			}
			wtB, err = sb.Edit(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			defer r.WorktreeRemove(ctx, wtB)
			if changed, err := r.HasChanges(ctx, wtB); err != nil || changed {
				t.Fatalf("recovery edit must be unchanged: %v %v", changed, err)
			}
			saved, err := sb.Save(ctx, "reconcile existing divergence", "origin", false)
			if conflict {
				var ce *ConflictError
				if !errors.As(err, &ce) {
					t.Fatalf("expected genuine merge conflict, got %+v %v", saved, err)
				}
				for i := 0; i < 3; i++ {
					if _, err := sb.Save(ctx, "still unresolved", "origin", false); !errors.As(err, &ce) {
						t.Fatalf("unresolved retry %d must remain conflict: %v", i, err)
					}
					if got := lsRemote(t, b, origin, refspace.WikiMainRef); got != remote.CommitHash {
						t.Fatalf("unresolved markers were published: %s", got)
					}
				}
				writeWiki(t, wtB, "Home.md", "resolved local and remote edits\n")
				saved, err = sb.Save(ctx, "resolved", "origin", false)
			}
			if err != nil || !saved.Pushed {
				t.Fatalf("unchanged edit must recover divergence: %+v %v", saved, err)
			}
			for _, parent := range []string{local.CommitHash, remote.CommitHash} {
				if ancestor, err := r.IsAncestor(ctx, parent, saved.CommitHash); err != nil || !ancestor {
					t.Fatalf("recovery dropped history %s: %v %v", parent, ancestor, err)
				}
			}
			assertNoWorktree(t, b, wtB)
		})
	}
}
