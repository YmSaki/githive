package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func wikiGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// seedWiki writes path with content, commits it, and advances
// refs/projects/wiki/main to HEAD — the raw-git equivalent of a future
// `githive wiki save`, so the read-side show/log commands have something to
// read before the write side exists.
func seedWiki(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wikiGit(t, dir, "add", path)
	wikiGit(t, dir, "commit", "-m", msg)
	wikiGit(t, dir, "update-ref", "refs/projects/wiki/main", "HEAD")
}

func TestCLIWikiShow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	seedWiki(t, dir, "Home.md", "# home page\n", "add Home")

	// Plain (non-json) show writes the raw bytes to stdout.
	res := runCLI(t, dir, "wiki", "show", "Home.md")
	if res.code != 0 {
		t.Fatalf("wiki show failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if res.stdout != "# home page\n" {
		t.Errorf("plain show stdout = %q", res.stdout)
	}

	// --json wraps path + content in the success envelope.
	res = runCLI(t, dir, "wiki", "show", "Home.md", "--json")
	if res.code != 0 {
		t.Fatalf("wiki show --json failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !env.OK || env.Data.Path != "Home.md" || env.Data.Content != "# home page\n" {
		t.Errorf("unexpected show --json: %s", res.stdout)
	}
}

func TestCLIWikiShowNotFound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	seedWiki(t, dir, "Home.md", "hi\n", "add Home")

	res := runCLI(t, dir, "wiki", "show", "Missing.md", "--json")
	if res.code != 1 {
		t.Fatalf("expected exit 1 (not_found), got %d (stdout=%s stderr=%s)", res.code, res.stdout, res.stderr)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if env.OK || env.Error.Code != "not_found" {
		t.Errorf("expected ok:false not_found, got %s", res.stdout)
	}
}

func TestCLIWikiLog(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	seedWiki(t, dir, "Home.md", "one\n", "first")
	seedWiki(t, dir, "design/sync.md", "sync\n", "second")

	res := runCLI(t, dir, "wiki", "log", "--json")
	if res.code != 0 {
		t.Fatalf("wiki log failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !env.OK || env.Data.Total != 2 || len(env.Data.Items) != 2 {
		t.Fatalf("expected 2 log items, got %s", res.stdout)
	}
	// Most-recent first, all fields present.
	if env.Data.Items[0]["subject"] != "second" {
		t.Errorf("expected newest first, got %v", env.Data.Items[0]["subject"])
	}
	for _, k := range []string{"hash", "author", "date", "subject"} {
		if v, ok := env.Data.Items[0][k].(string); !ok || v == "" {
			t.Errorf("log item missing key %q: %v", k, env.Data.Items[0])
		}
	}

	// Path filter narrows to touching commits.
	res = runCLI(t, dir, "wiki", "log", "Home.md", "--json")
	if res.code != 0 {
		t.Fatalf("wiki log <path> failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if env.Data.Total != 1 || env.Data.Items[0]["subject"] != "first" {
		t.Errorf("path-filtered log = %s", res.stdout)
	}
}

func TestCLIWikiLogEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	// No wiki ref yet → empty list, exit 0 (a repo may have no wiki).
	res := runCLI(t, dir, "wiki", "log", "--json")
	if res.code != 0 {
		t.Fatalf("wiki log on empty wiki failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var env struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if env.Data.Total != 0 {
		t.Errorf("expected empty log for no-wiki repo, got %s", res.stdout)
	}
}

// TestCLIWikiEditRefusesWhileInProgress is the CLI-level regression test for
// issue #57: `wiki edit` run twice without an intervening `wiki save` must
// not silently leak the first worktree — the second invocation errors with
// the wiki_worktree_exists code (exit 1) and names the first worktree.
func TestCLIWikiEditRefusesWhileInProgress(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	first := runCLI(t, dir, "wiki", "edit", "--json")
	if first.code != 0 {
		t.Fatalf("first wiki edit failed: code=%d stderr=%s", first.code, first.stderr)
	}
	var firstEnv struct {
		Data struct {
			Worktree string `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(first.stdout), &firstEnv); err != nil {
		t.Fatalf("bad json: %v\n%s", err, first.stdout)
	}
	if firstEnv.Data.Worktree == "" {
		t.Fatalf("expected a worktree path, got %s", first.stdout)
	}

	second := runCLI(t, dir, "wiki", "edit", "--json")
	if second.code != 1 {
		t.Fatalf("expected exit 1 (wiki_worktree_exists), got %d (stdout=%s stderr=%s)", second.code, second.stdout, second.stderr)
	}
	var errEnv struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(second.stdout), &errEnv); err != nil {
		t.Fatalf("bad json: %v\n%s", err, second.stdout)
	}
	if errEnv.OK || errEnv.Error.Code != "wiki_worktree_exists" {
		t.Errorf("expected ok:false wiki_worktree_exists, got %s", second.stdout)
	}
	if !strings.Contains(errEnv.Error.Message, firstEnv.Data.Worktree) {
		t.Errorf("expected error message to name the existing worktree %q, got %q", firstEnv.Data.Worktree, errEnv.Error.Message)
	}

	// The first worktree is untouched: still on disk and still registered.
	if _, statErr := os.Stat(firstEnv.Data.Worktree); statErr != nil {
		t.Fatalf("first worktree should still exist: %v", statErr)
	}
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if !strings.Contains(string(out), firstEnv.Data.Worktree) {
		t.Fatalf("first worktree should still be registered:\n%s", out)
	}
}

// TestWikiShowJSONBinaryUsesBase64 covers issue #53 at the level the bug
// lived: valid UTF-8 keeps the `content` key, and anything else moves to
// `content_base64` with `content` absent, so a client can never read
// U+FFFD-mangled bytes out of `content`.
func TestWikiShowJSONBinaryUsesBase64(t *testing.T) {
	t.Run("valid utf8", func(t *testing.T) {
		out := wikiShowJSON("guide.md", []byte("日本語 text\n"))
		if got := out["content"]; got != "日本語 text\n" {
			t.Errorf("content = %q", got)
		}
		if _, ok := out["content_base64"]; ok {
			t.Error("content_base64 should be absent for valid UTF-8")
		}
	})

	t.Run("invalid utf8", func(t *testing.T) {
		raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0xff, 0xfe}
		out := wikiShowJSON("_assets/logo.png", raw)
		if _, ok := out["content"]; ok {
			t.Error("content should be omitted for non-UTF-8 bytes")
		}
		encoded, ok := out["content_base64"].(string)
		if !ok {
			t.Fatalf("content_base64 missing or not a string: %#v", out["content_base64"])
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != string(raw) {
			t.Errorf("round-trip lost bytes: %v -> %v", raw, decoded)
		}
	})
}

// TestCLIWikiShowBinaryJSON is the same guarantee end to end, through a real
// wiki page holding bytes that are not valid UTF-8.
func TestCLIWikiShowBinaryJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe}
	seedWiki(t, dir, "_assets/logo.png", string(raw), "add binary asset")

	res := runCLI(t, dir, "wiki", "show", "_assets/logo.png", "--json")
	if res.code != 0 {
		t.Fatalf("wiki show failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var shown struct {
		Data struct {
			Content       *string `json:"content"`
			ContentBase64 string  `json:"content_base64"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &shown); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if shown.Data.Content != nil {
		t.Errorf("content should be absent, got %q", *shown.Data.Content)
	}
	decoded, err := base64.StdEncoding.DecodeString(shown.Data.ContentBase64)
	if err != nil {
		t.Fatalf("content_base64 is not valid base64: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Errorf("round-trip lost bytes:\n got %v\nwant %v", decoded, raw)
	}
}
