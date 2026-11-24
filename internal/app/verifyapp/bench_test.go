package verifyapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func BenchmarkVerifyAll(b *testing.B) {
	dir := b.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           100,
		CommentsPerIssue: 5,
	}); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reports, err := VerifyAll(ctx, dir, "")
		if err != nil {
			b.Fatal(err)
		}
		if len(reports) != 100 {
			b.Fatalf("got %d reports, want 100", len(reports))
		}
	}
}
