package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
