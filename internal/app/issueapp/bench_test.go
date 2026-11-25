package issueapp

import (
	"context"
	"os"
	"path/filepath"
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
	dbPath := filepath.Join(dir, ".git", "githive-cache.db")

	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			_ = os.Remove(dbPath)
			b.StartTimer()
			items, err := svc.List(ctx, ListFilter{})
			if err != nil {
				b.Fatal(err)
			}
			if len(items) != 200 {
				b.Fatalf("got %d items, want 200", len(items))
			}
		}
	})

	b.Run("warm", func(b *testing.B) {
		if _, err := svc.List(ctx, ListFilter{}); err != nil {
			b.Fatal(err)
		}
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
	})
}
