package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain builds the githive binary once and shares it across all tests in
// this package (docs/14-testing.md「E2E」: CLI をサブプロセス実行し JSON を
// 検証する).
var binPath string

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("git"); err != nil {
		os.Exit(m.Run()) // tests below individually skip if git is missing
	}
	dir, err := os.MkdirTemp("", "githive-cli-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binPath = filepath.Join(dir, "githive")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		panic("go build: " + err.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

type cliResult struct {
	code   int
	stdout string
	stderr string
}

func runCLI(t *testing.T, dir string, args ...string) cliResult {
	t.Helper()
	if binPath == "" {
		t.Skip("git binary not available; CLI binary was not built")
	}
	args = append(args, "--repo", dir)
	cmd := exec.Command(binPath, args...)
	var stdout, stderr []byte
	outPipe, _ := cmd.StdoutPipe()
	errPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdout, _ = io.ReadAll(outPipe)
	stderr, _ = io.ReadAll(errPipe)
	err := cmd.Wait()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return cliResult{code: code, stdout: string(stdout), stderr: string(stderr)}
}

func newCLITestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", dir},
		{"-C", dir, "config", "user.email", "cli@example.com"},
		{"-C", dir, "config", "user.name", "CLI"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCLIIssueLifecycleJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "issue", "new", "--title", "t1", "--body", "b1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		OK   bool `json:"ok"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !created.OK || created.Data.ID == "" {
		t.Fatalf("unexpected response: %+v", created)
	}
	id := created.Data.ID

	res = runCLI(t, dir, "issue", "show", id, "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue show failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var shown struct {
		OK   bool `json:"ok"`
		Data struct {
			Meta map[string]any `json:"meta"`
			Body string         `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &shown); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if shown.Data.Meta["title"] != "t1" || shown.Data.Body != "b1" {
		t.Errorf("unexpected show data: %+v", shown.Data)
	}

	// Not found -> exit code 1, ok:false envelope.
	res = runCLI(t, dir, "issue", "show", "01j8x0a2b3c4d5e6f7g8h9j0ka", "--no-sync", "--json")
	if res.code != 1 {
		t.Errorf("expected exit code 1 for not-found, got %d", res.code)
	}
	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &failed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if failed.OK || failed.Error.Code != "not_found" {
		t.Errorf("unexpected failure envelope: %+v", failed)
	}
}

func TestCLINotARepoExitsFive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	if binPath == "" {
		t.Skip("cli binary not built")
	}
	dir := t.TempDir() // no .git here
	cmd := exec.Command(binPath, "whoami", "--repo", dir)
	out, err := cmd.CombinedOutput()
	_ = out
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 5 {
		t.Errorf("expected exit code 5, got err=%v out=%s", err, out)
	}
}
