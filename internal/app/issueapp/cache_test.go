package issueapp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ymsaki/githive/internal/testutil/synthrepo"
)

func TestListCacheConsistencyAndCorruptionFallback(t *testing.T) {
	dir := t.TempDir()
	if _, err := synthrepo.Generate(dir, synthrepo.Config{
		Issues:           5,
		CommentsPerIssue: 2,
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(dir)
	ctx := context.Background()

	// 1. Cold read (populates .git/githive-cache.db)
	cold, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cold) != 5 {
		t.Fatalf("got %d issues, want 5", len(cold))
	}

	// 2. Warm read (hits cache)
	warm, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("warm cache mismatch:\ncold=%+v\nwarm=%+v", cold, warm)
	}

	// 3. Corrupt cache file and verify transparent recovery
	dbPath := filepath.Join(dir, ".git", "githive-cache.db")
	if err := os.WriteFile(dbPath, []byte("corrupt-sqlite-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, recovered) {
		t.Fatalf("recovered cache mismatch:\ncold=%+v\nrecovered=%+v", cold, recovered)
	}
}
