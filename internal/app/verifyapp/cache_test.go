package verifyapp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func TestVerifyAllCacheConsistencyAndCorruptionFallback(t *testing.T) {
	dir := t.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           3,
		CommentsPerIssue: 2,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	cold, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	warm, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("warm cache mismatch:\ncold=%+v\nwarm=%+v", cold, warm)
	}

	dbPath := filepath.Join(dir, ".git", "githive-cache.db")
	if err := os.WriteFile(dbPath, []byte("corrupt-sqlite-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, recovered) {
		t.Fatalf("recovered cache mismatch:\ncold=%+v\nrecovered=%+v", cold, recovered)
	}
}
