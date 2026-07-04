package chain

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initBareDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func testEnvelope(id, entity, kind string, data map[string]any) *event.Envelope {
	return &event.Envelope{
		V:      1,
		Kind:   kind,
		ID:     id,
		TS:     "2026-07-04T12:00:00.000Z",
		Actor:  "tester@example.com",
		Entity: entity,
		Data:   data,
		Extra:  map[string]any{},
	}
}

func TestAppendEventAndWalkChain(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}

	entity := "01j8x0a2b3c4d5e6f7g8h9j0ka"
	env1 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t61", entity, "issue.create", map[string]any{"title": "first"})
	h1, err := AppendEvent(repo, ZeroHash, env1, "issue.create: first", map[string][]byte{
		"meta.json": []byte(`{"title":"first"}` + "\n"),
	}, sig)
	if err != nil {
		t.Fatal(err)
	}

	env2 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t62", entity, "issue.comment", map[string]any{"body": "hi"})
	h2, err := AppendEvent(repo, h1, env2, "issue.comment: hi", map[string][]byte{
		"meta.json":              []byte(`{"title":"first"}` + "\n"),
		"comments/c1.md":         []byte("hi\n"),
		"comments/nested/x.json": []byte(`{}` + "\n"),
	}, sig)
	if err != nil {
		t.Fatal(err)
	}

	refName := plumbing.ReferenceName("refs/projects/issue/" + entity)
	if err := AdvanceRef(dir, refName, h2, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	envelopes, err := WalkChain(repo, h2)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envelopes))
	}
	seen := map[string]bool{}
	for _, e := range envelopes {
		seen[e.ID] = true
	}
	if !seen[env1.ID] || !seen[env2.ID] {
		t.Errorf("missing envelopes: %+v", envelopes)
	}

	files, err := ReadTree(repo, h2)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["comments/c1.md"]) != "hi\n" {
		t.Errorf("unexpected comments/c1.md: %q", files["comments/c1.md"])
	}
	if string(files["comments/nested/x.json"]) != "{}\n" {
		t.Errorf("unexpected nested file: %q", files["comments/nested/x.json"])
	}
	if string(files["meta.json"]) != `{"title":"first"}`+"\n" {
		t.Errorf("unexpected meta.json: %q", files["meta.json"])
	}
}

func TestAdvanceRefCASRejectsStaleOld(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0kb"
	env := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t63", entity, "task.create", map[string]any{"title": "t"})
	h1, err := AppendEvent(repo, ZeroHash, env, "task.create: t", map[string][]byte{"meta.json": []byte("{}\n")}, sig)
	if err != nil {
		t.Fatal(err)
	}
	refName := plumbing.ReferenceName("refs/projects/task/" + entity)
	if err := AdvanceRef(dir, refName, h1, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	env2 := testEnvelope("01j8xq4d3nbz9k7w2m5e8h1t64", entity, "task.note", map[string]any{"body": "n"})
	h2, err := AppendEvent(repo, h1, env2, "task.note: n", map[string][]byte{"meta.json": []byte("{}\n")}, sig)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong old OID must be rejected.
	if err := AdvanceRef(dir, refName, h2, gitx.ZeroOID); err == nil {
		t.Error("expected CAS rejection with stale old OID")
	}
	// Correct old OID succeeds.
	if err := AdvanceRef(dir, refName, h2, h1.String()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTreeDeterministic(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"b.json": []byte("{}\n"),
		"a/z.md": []byte("z\n"),
		"a/y.md": []byte("y\n"),
	}
	h1, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := WriteTree(repo, files)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("expected identical tree hash for identical inputs, got %s vs %s", h1, h2)
	}
}

func TestOpenRepositoryMissingDir(t *testing.T) {
	if _, err := OpenRepository(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error opening nonexistent repository")
	}
}
