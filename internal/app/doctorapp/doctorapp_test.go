package doctorapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ymsaki/githive/internal/app/issueapp"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// newRepo makes a repo with a local user.email so identity resolves ok by
// default; tests that want the identity-error path use initRepo + isolated
// global config instead.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	runGitIn(t, dir, "config", "user.email", "tester@example.com")
	runGitIn(t, dir, "config", "user.name", "Tester")
	return dir
}

// isolateGitConfig points git at empty global/system config files so a test
// asserting the *absence* of a config value is not fooled by the developer's
// real global git config (git config --get falls back to global/system).
func isolateGitConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	empty := filepath.Join(dir, "cfg")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s: %v\n%s", ref, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReportHasError(t *testing.T) {
	if (Report{Checks: []Check{{Severity: SeverityOK}, {Severity: SeverityWarning}}}).HasError() {
		t.Error("report with only ok/warning checks must not report HasError")
	}
	if !(Report{Checks: []Check{{Severity: SeverityWarning}, {Severity: SeverityError}}}).HasError() {
		t.Error("report containing an error check must report HasError")
	}
}

func TestCheckGitVersion(t *testing.T) {
	requireGit(t)
	c := New(t.TempDir()).checkGitVersion(context.Background())
	if c.Severity != SeverityOK {
		t.Errorf("supported system git should be ok, got %+v", c)
	}
}

func TestCheckRefspec(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	ctx := context.Background()
	svc := New(dir)

	if c := svc.checkRefspec(ctx, "origin"); c.Severity != SeverityWarning {
		t.Errorf("fresh repo without tracking refspec should warn, got %+v", c)
	}
	runGitIn(t, dir, "config", "--add", "remote.origin.fetch", "+refs/projects/*:refs/githive-remote/*")
	if c := svc.checkRefspec(ctx, "origin"); c.Severity != SeverityOK {
		t.Errorf("repo with tracking refspec should be ok, got %+v", c)
	}
}

func TestCheckIdentityOK(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	if c := New(dir).checkIdentity(context.Background()); c.Severity != SeverityOK {
		t.Errorf("repo with user.email should be ok, got %+v", c)
	}
}

func TestCheckIdentityMissing(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	dir := t.TempDir()
	initRepo(t, dir) // no user.email, and global/system are isolated-empty
	if c := New(dir).checkIdentity(context.Background()); c.Severity != SeverityError {
		t.Errorf("repo without user.email should be an error, got %+v", c)
	}
}

func TestCheckSigning(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	dir := t.TempDir()
	initRepo(t, dir)
	ctx := context.Background()
	svc := New(dir)

	if c := svc.checkSigning(ctx); c.Severity != SeverityWarning {
		t.Errorf("unset signing should warn, got %+v", c)
	}

	runGitIn(t, dir, "config", "commit.gpgsign", "true")
	runGitIn(t, dir, "config", "gpg.format", "ssh")
	runGitIn(t, dir, "config", "user.signingkey", "/home/u/.ssh/id_ed25519.pub")
	if c := svc.checkSigning(ctx); c.Severity != SeverityOK {
		t.Errorf("complete ssh signing config should be ok, got %+v", c)
	}

	runGitIn(t, dir, "config", "--unset", "user.signingkey")
	if c := svc.checkSigning(ctx); c.Severity != SeverityWarning {
		t.Errorf("gpgsign on but incomplete config should warn, got %+v", c)
	}
}

func TestCheckClockSkewNoRemote(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	if c := New(dir).checkClockSkew(context.Background()); c.Severity != SeverityOK {
		t.Errorf("no remote-tracking events should be ok, got %+v", c)
	}
}

func TestCheckClockSkew(t *testing.T) {
	requireGit(t)
	dir := newRepo(t)
	ctx := context.Background()

	// Create a real event chain, then mirror it into the remote-tracking
	// namespace so latestRemoteTS finds it.
	id, err := issueapp.New(dir).NewIssue(ctx, "clock-test", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oid := revParse(t, dir, "refs/projects/issue/"+id)
	runGitIn(t, dir, "update-ref", "refs/githive-remote/issue/"+id, oid)

	svc := New(dir)
	latest, err := svc.latestRemoteTS(ctx)
	if err != nil || latest == "" {
		t.Fatalf("latestRemoteTS = %q, %v; want a non-empty ts", latest, err)
	}
	latestT, err := time.Parse(tsLayout, latest)
	if err != nil {
		t.Fatalf("parse latest ts %q: %v", latest, err)
	}

	// Local clock 10 minutes behind the remote event -> warning.
	svc.nowFn = func() time.Time { return latestT.Add(-10 * time.Minute) }
	if c := svc.checkClockSkew(ctx); c.Severity != SeverityWarning {
		t.Errorf("clock 10m behind remote event should warn, got %+v", c)
	}

	// Local clock at the event time -> ok.
	svc.nowFn = func() time.Time { return latestT }
	if c := svc.checkClockSkew(ctx); c.Severity != SeverityOK {
		t.Errorf("clock at remote event time should be ok, got %+v", c)
	}
}

func TestDiagnoseAlwaysReturnsFiveChecksAndFlagsError(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	dir := t.TempDir()
	initRepo(t, dir) // missing identity -> one error check

	report, err := New(dir).Diagnose(context.Background(), "origin")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 5 {
		t.Errorf("expected 5 checks, got %d: %+v", len(report.Checks), report.Checks)
	}
	if !report.HasError() {
		t.Errorf("missing identity should make the report HasError, got %+v", report.Checks)
	}
}
