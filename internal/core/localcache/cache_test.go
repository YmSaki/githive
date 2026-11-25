package localcache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
)

func TestCacheCRUDAndRecovery(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}

	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = c.SyncRefMeta([]RefMetaEntry{
		{
			RefName:  "refs/projects/issue/01abc",
			HeadOID:  "oid1",
			Feature:  "issue",
			EntityID: "01abc",
			MetaJSON: []byte(`{"id":"01abc"}`),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.GetRefMetaByPrefix("refs/projects/issue/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got["refs/projects/issue/01abc"].MetaJSON, []byte(`{"id":"01abc"}`)) {
		t.Fatalf("unexpected ref meta: %+v", got)
	}

	if err := c.PutSigVerifications([]SigEntry{{
		CommitOID: "c1",
		SigValid:  true,
		PubKeyFP:  "SHA256:abc",
		KeyType:   "ssh-ed25519",
	}}); err != nil {
		t.Fatal(err)
	}
	sigs, err := c.GetSigVerifications([]string{"c1", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || !sigs["c1"].SigValid || sigs["c1"].PubKeyFP != "SHA256:abc" {
		t.Fatalf("unexpected sigs: %+v", sigs)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Corrupt cache file and verify automatic recovery.
	dbPath := filepath.Join(dir, ".git", cacheFileName)
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after corruption failed: %v", err)
	}
	got2, err := c2.GetRefMetaByPrefix("refs/projects/issue/")
	if err != nil || len(got2) != 0 {
		t.Fatalf("expected empty recovered cache, got %+v err=%v", got2, err)
	}
	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}
}
