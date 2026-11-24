package issueapp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ymsaki/githive/internal/core/localcache"
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

	// 4. Corrupt one cached row payload and verify transparent fallback & repair
	c, err := localcache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := c.GetRefMetaByPrefix("refs/projects/issue/")
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected cached entries, got %d err=%v", len(entries), err)
	}
	for _, e := range entries {
		e.MetaJSON = []byte("{broken-json")
		if err := c.SyncRefMeta([]localcache.RefMetaEntry{e}, nil); err != nil {
			t.Fatal(err)
		}
		break
	}
	_ = c.Close()

	repaired, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, repaired) {
		t.Fatalf("repaired cache mismatch:\ncold=%+v\nrepaired=%+v", cold, repaired)
	}

	// 5. Tamper cached payload with valid JSON and verify SHA-256 mismatch fallback
	c2, err := localcache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries2, err := c2.GetRefMetaByPrefix("refs/projects/issue/")
	if err != nil || len(entries2) == 0 {
		t.Fatalf("expected cached entries, got %d err=%v", len(entries2), err)
	}
	for _, e := range entries2 {
		e.MetaJSON = []byte(`{"id":"tampered","title":"wrong","status":"closed"}`)
		if err := c2.SyncRefMeta([]localcache.RefMetaEntry{e}, nil); err != nil {
			t.Fatal(err)
		}
		break
	}
	_ = c2.Close()

	afterTamper, err := svc.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, afterTamper) {
		t.Fatalf("tampered-JSON cache mismatch:\ncold=%+v\nafterTamper=%+v", cold, afterTamper)
	}
}
