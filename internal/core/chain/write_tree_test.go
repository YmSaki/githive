package chain

import (
	"fmt"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage"
)

type countingStorer struct {
	storage.Storer
	setCalls int
}

func (c *countingStorer) SetEncodedObject(obj plumbing.EncodedObject) (plumbing.Hash, error) {
	c.setCalls++
	return c.Storer.SetEncodedObject(obj)
}

func TestWriteTreeSkipsUnchangedObjects(t *testing.T) {
	dir := t.TempDir()
	rawRepo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	cs := &countingStorer{Storer: rawRepo.Storer}
	repo := &git.Repository{Storer: cs}

	files := map[string][]byte{
		"meta.json": []byte(`{"id":"1"}`),
	}
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("comments/%03d.json", i)] = []byte(fmt.Sprintf(`{"n":%d}`, i))
	}

	h1, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	if cs.setCalls < 100 {
		t.Fatalf("first WriteTree wrote %d objects, expected >= 100", cs.setCalls)
	}

	cs.setCalls = 0
	files["comments/100.json"] = []byte(`{"n":100}`)
	h2, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	if h2 == h1 {
		t.Fatalf("tree hash did not change after adding file")
	}
	// Only 1 new blob + comments subtree + root tree = 3 objects should be written.
	if cs.setCalls != 3 {
		t.Fatalf("second WriteTree wrote %d objects, want 3", cs.setCalls)
	}

	// Writing the exact same files again should write 0 objects and return identical hash.
	cs.setCalls = 0
	h3, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	if h3 != h2 {
		t.Fatalf("non-deterministic tree hash: %s != %s", h3, h2)
	}
	if cs.setCalls != 0 {
		t.Fatalf("third WriteTree wrote %d objects, want 0", cs.setCalls)
	}
}
