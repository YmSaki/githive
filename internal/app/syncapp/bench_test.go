package syncapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func BenchmarkUnpushedRefs(b *testing.B) {
	dir := b.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:               500,
		CommentsPerIssue:     0,
		MirrorRemoteTracking: true,
	}); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		unpushed, err := UnpushedRefs(ctx, dir)
		if err != nil {
			b.Fatal(err)
		}
		if len(unpushed) != 0 {
			b.Fatalf("got %d unpushed, want 0", len(unpushed))
		}
	}
}

func BenchmarkSyncUpToDate(b *testing.B) {
	dir := b.TempDir()
	res, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:               200,
		CommentsPerIssue:     5,
		MirrorRemoteTracking: true,
	})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := Sync(ctx, dir, "origin", res.IssueRefs, 0, SkipInitialFetch())
		if err != nil {
			b.Fatal(err)
		}
		if len(results) != len(res.IssueRefs) {
			b.Fatalf("got %d results, want %d", len(results), len(res.IssueRefs))
		}
	}
}
