package issueapp

import (
	"bytes"
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
	var tamperedRef, tamperedBlobOID string
	var origMetaJSON []byte
	var origMetaSHA256 string
	for _, e := range entries2 {
		tamperedRef = e.RefName
		tamperedBlobOID = e.MetaBlobOID
		origMetaJSON = append([]byte(nil), e.MetaJSON...)
		origMetaSHA256 = e.MetaSHA256
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

	// Verify the DB row itself was repaired with the original Git content.
	c3, err := localcache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries3, err := c3.GetRefMetaByPrefix("refs/projects/issue/")
	_ = c3.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := entries3[tamperedRef]; !bytes.Equal(got.MetaJSON, origMetaJSON) || got.MetaSHA256 != origMetaSHA256 {
		t.Fatalf("cache row not repaired: got sha=%q, want sha=%q", got.MetaSHA256, origMetaSHA256)
	}

	// 6. Remove the loose meta.json blob object from .git/objects and confirm
	// warm List fails with an object-not-found error just like cold List.
	blobPath := filepath.Join(dir, ".git", "objects", tamperedBlobOID[:2], tamperedBlobOID[2:])
	if err := os.Chmod(blobPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blobPath); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, ListFilter{}); err == nil {
		t.Fatalf("expected warm List to fail when Git blob %s is missing", tamperedBlobOID)
	}
	_ = os.Remove(dbPath)
	if _, err := svc.List(ctx, ListFilter{}); err == nil {
		t.Fatalf("expected cold List to fail when Git blob %s is missing", tamperedBlobOID)
	}
}
