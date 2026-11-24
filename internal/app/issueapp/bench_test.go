package issueapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func BenchmarkListIssues(b *testing.B) {
	dir := b.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           200,
		CommentsPerIssue: 10,
	}); err != nil {
		b.Fatal(err)
	}
	svc := New(dir)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := svc.List(ctx, ListFilter{})
		if err != nil {
			b.Fatal(err)
		}
		if len(items) != 200 {
			b.Fatalf("got %d items, want 200", len(items))
		}
	}
}
