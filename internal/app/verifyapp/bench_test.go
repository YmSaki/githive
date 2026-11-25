package verifyapp

import (
	"context"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func BenchmarkVerifyAllSigned(b *testing.B) {
	dir := b.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           100,
		CommentsPerIssue: 5,
		SignWithEd25519:  true,
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
		// 100 issues + 1 users/registry
		if len(reports) != 101 {
			b.Fatalf("got %d reports, want 101", len(reports))
		}
		for _, r := range reports {
			if !r.OK() {
				b.Fatalf("ref %s failed verify: %+v", r.Ref, r.Issues)
			}
		}
	}
}
