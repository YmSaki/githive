package fsckapp

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
)

func TestMaterializationDetectsSymlinkModeTampering(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	id, err := issueapp.New(dir).NewIssue(ctx, "original", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := New(dir).Run(ctx, Options{})
	if err != nil || !clean.OK() {
		t.Fatalf("unmodified tree must pass fsck: report=%+v err=%v", clean, err)
	}
	ref := "refs/projects/issue/" + id
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := object.GetCommit(repo.Storer, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	for i := range tree.Entries {
		if tree.Entries[i].Name == "meta.json" {
			tree.Entries[i].Mode = filemode.Symlink
		}
	}
	enc := repo.Storer.NewEncodedObject()
	if err := tree.Encode(enc); err != nil {
		t.Fatal(err)
	}
	treeHash, err := repo.Storer.SetEncodedObject(enc)
	if err != nil {
		t.Fatal(err)
	}
	commit.TreeHash = treeHash
	enc = repo.Storer.NewEncodedObject()
	if err := commit.Encode(enc); err != nil {
		t.Fatal(err)
	}
	forged, err := repo.Storer.SetEncodedObject(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, ref, forged.String(), oid); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsWithCode(report, "tree_mismatch"); len(got) != 1 || got[0].Ref != ref {
		t.Fatalf("want one tree_mismatch for tampered meta.json filemode (regular -> symlink): %+v", report)
	}
}
