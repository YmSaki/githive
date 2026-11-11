package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRevisionQueriesDistinguishAbsentFromRepositoryFailure(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	ctx := context.Background()
	if oid, err := New(dir).RevParse(ctx, "refs/projects/meta/config"); oid != "" || err != nil {
		t.Fatalf("missing ref: %q %v", oid, err)
	}
	if merging, err := New(dir).MergeInProgress(ctx, dir); merging || err != nil {
		t.Fatalf("absent MERGE_HEAD: %t %v", merging, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, badDir := range []string{dir, t.TempDir()} {
		if _, err := New(badDir).RevParse(ctx, "refs/projects/meta/config"); err == nil {
			t.Fatal("failed revision query treated as missing ref")
		}
		if _, err := New(badDir).MergeInProgress(ctx, badDir); err == nil {
			t.Fatal("failed merge query treated as no merge")
		}
	}
}
