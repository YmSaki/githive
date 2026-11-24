package chain

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/ymsaki/githive/internal/core/event"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"meta.json": []byte(`{"id":"01jzamhbw03hfk7nkqbsqvfxvz"}`),
	}
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("comments/%03d.json", i)] = []byte(fmt.Sprintf(`{"n":%d}`, i))
	}
	env := &event.Envelope{
		V:      event.SchemaVersion,
		Kind:   "issue.create",
		ID:     "01jzamhbw03hfk7nkqbsqvfxvz",
		TS:     "2026-01-01T00:00:00.000Z",
		Actor:  "a@example.com",
		Entity: "01jzamhbw03hfk7nkqbsqvfxvz",
		Data:   map[string]any{"title": "t"},
	}
	sig := object.Signature{Name: "a", Email: "a@example.com", When: time.Now().UTC()}
	h, err := AppendEvent(repo, ZeroHash, env, "init", files, sig)
	if err != nil {
		t.Fatal(err)
	}

	gotMeta, ok, err := ReadFile(repo, h, "meta.json")
	if err != nil || !ok {
		t.Fatalf("ReadFile meta.json: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(gotMeta, files["meta.json"]) {
		t.Fatalf("meta.json mismatch: got %q want %q", gotMeta, files["meta.json"])
	}

	gotComment, ok, err := ReadFile(repo, h, "comments/042.json")
	if err != nil || !ok {
		t.Fatalf("ReadFile comments/042.json: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(gotComment, files["comments/042.json"]) {
		t.Fatalf("comment mismatch")
	}

	if _, ok, err := ReadFile(repo, h, "missing.json"); err != nil || ok {
		t.Fatalf("missing.json: got ok=%v err=%v, want false, nil", ok, err)
	}
	if _, ok, err := ReadFile(repo, h, "comments"); err != nil || ok {
		t.Fatalf("directory path: got ok=%v err=%v, want false, nil", ok, err)
	}
}
