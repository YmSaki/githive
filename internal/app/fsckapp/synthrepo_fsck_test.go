package fsckapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func TestSynthRepoPassesFsck(t *testing.T) {
	dir := t.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           1,
		CommentsPerIssue: 5,
		SignWithEd25519:  true,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("synthrepo failed fsck: %+v", report.Findings)
	}
}
