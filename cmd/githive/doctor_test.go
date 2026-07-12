package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCLIDoctorWarningsExitZero: a repo with a valid identity but no
// tracking refspec/signing produces only warnings, so doctor exits 0 with
// an ok:true envelope carrying every check.
func TestCLIDoctorWarningsExitZero(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // sets user.email locally

	res := runCLI(t, dir, "doctor", "--json")
	if res.code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s stdout=%s", res.code, res.stderr, res.stdout)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Checks []struct {
				Name     string `json:"name"`
				Severity string `json:"severity"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if !env.OK {
		t.Errorf("expected ok:true, got %s", res.stdout)
	}
	if len(env.Data.Checks) != 5 {
		t.Errorf("expected 5 checks, got %d: %s", len(env.Data.Checks), res.stdout)
	}
	for _, c := range env.Data.Checks {
		if c.Name == "identity" && c.Severity != "ok" {
			t.Errorf("identity should be ok when user.email is set, got %q", c.Severity)
		}
		if c.Severity == "error" {
			t.Errorf("no check should be error in this repo, got error on %q", c.Name)
		}
	}
}

// TestCLIDoctorEnvironmentUnhealthyExitsFive: a repo with no resolvable
// identity (isolated empty global/system config) makes doctor exit 5 with an
// ok:false, code=environment_unhealthy envelope (docs/10「終了コード」5).
func TestCLIDoctorEnvironmentUnhealthyExitsFive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	if binPath == "" {
		t.Skip("cli binary not built")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	empty := filepath.Join(t.TempDir(), "cfg")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "doctor", "--json", "--repo", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+empty, "GIT_CONFIG_SYSTEM="+empty)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("unexpected exec error: %v\n%s", err, out)
	}
	if code != 5 {
		t.Fatalf("expected exit 5, got %d\n%s", code, out)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if env.OK || env.Error.Code != "environment_unhealthy" {
		t.Errorf("unexpected failure envelope: %+v (%s)", env, out)
	}
}
