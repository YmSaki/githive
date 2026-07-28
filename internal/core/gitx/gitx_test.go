package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out.String())
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "--quiet")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
}

func commitEmpty(t *testing.T, dir, msg string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(out))
}

func TestCheckVersion(t *testing.T) {
	requireGit(t)
	v, err := CheckVersion(context.Background())
	if err != nil {
		t.Fatalf("expected a supported git version, got error: %v", err)
	}
	if v.Major == 0 {
		t.Errorf("parsed zero version: %+v", v)
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]Version{
		"git version 2.43.0":           {2, 43, 0},
		"git version 2.34.1.windows.1": {2, 34, 1},
	}
	for input, want := range cases {
		got, err := parseVersion(input)
		if err != nil {
			t.Errorf("%q: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %+v want %+v", input, got, want)
		}
	}
	if _, err := parseVersion("nonsense"); err == nil {
		t.Error("expected error for unparsable version string")
	}
}

func TestForEachRefAndRevParse(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	oid := commitEmpty(t, dir, "root")
	if err := r.UpdateRef(ctx, "refs/projects/issue/xyz", oid, ZeroOID); err != nil {
		t.Fatal(err)
	}

	entries, err := r.ForEachRef(ctx, "refs/projects/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Ref != "refs/projects/issue/xyz" || entries[0].OID != oid {
		t.Fatalf("got %+v", entries)
	}

	got, err := r.RevParse(ctx, "refs/projects/issue/xyz")
	if err != nil {
		t.Fatal(err)
	}
	if got != oid {
		t.Errorf("got %q want %q", got, oid)
	}

	missing, err := r.RevParse(ctx, "refs/projects/issue/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if missing != "" {
		t.Errorf("expected empty string for missing ref, got %q", missing)
	}
}

// TestIsRefUpdateRetryable pins the classification isRefUpdateRetryable does
// on real git update-ref failure text (docs/10-cli-spec.md「終了コード」):
// CAS races (current value mismatch, lock contention) are retryable; every
// other failure is a permanent one entitychain must not loop on.
func TestIsRefUpdateRetryable(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			"cas mismatch, existing ref",
			"fatal: update_ref failed for ref 'refs/projects/issue/xyz': cannot lock ref 'refs/projects/issue/xyz': is at 5bb...15a5 but expected 111...111",
			true,
		},
		{
			"cas mismatch, old value given but ref does not exist",
			"fatal: update_ref failed for ref 'refs/projects/issue/xyz': cannot lock ref 'refs/projects/issue/xyz': unable to resolve reference 'refs/projects/issue/xyz'",
			true,
		},
		{
			"lock contention",
			"fatal: update_ref failed for ref 'refs/projects/issue/xyz': cannot lock ref 'refs/projects/issue/xyz': Unable to create '.../xyz.lock': File exists.",
			true,
		},
		{
			"bad ref name",
			"fatal: update_ref failed for ref 'refs/projects/issue/bad ref name': refusing to update ref with bad name 'refs/projects/issue/bad ref name'",
			false,
		},
		{
			"unrecognized failure text",
			"fatal: some future git version's brand new error message",
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRefUpdateRetryable(c.output); got != c.want {
				t.Errorf("isRefUpdateRetryable(%q) = %v, want %v", c.output, got, c.want)
			}
		})
	}
}

// TestUpdateRefBadRefNameIsNotCASMismatch guards the fix for A4: a
// permanently-failing update-ref (here, a ref name git refuses outright)
// must not come back as ErrRefCASMismatch, or entitychain
// (internal/app/entitychain) would retry it defaultRetries times for no
// reason instead of failing once.
func TestUpdateRefBadRefNameIsNotCASMismatch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	oid := commitEmpty(t, dir, "root")
	err := r.UpdateRef(ctx, "refs/projects/issue/bad ref name", oid, ZeroOID)
	if err == nil {
		t.Fatal("expected an error for a malformed ref name")
	}
	if errors.Is(err, ErrRefCASMismatch) {
		t.Errorf("bad ref name must not be classified as ErrRefCASMismatch, got: %v", err)
	}
}

// TestDeleteRefBadOldOIDIsCASMismatch is the CAS-mismatch counterpart:
// DeleteRef with a stale expected value must still classify as
// ErrRefCASMismatch so callers keep retrying it.
func TestDeleteRefBadOldOIDIsCASMismatch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	oid := commitEmpty(t, dir, "root")
	if err := r.UpdateRef(ctx, "refs/projects/issue/xyz", oid, ZeroOID); err != nil {
		t.Fatal(err)
	}
	wrongOld := "1111111111111111111111111111111111111111"
	err := r.DeleteRef(ctx, "refs/projects/issue/xyz", wrongOld)
	if err == nil {
		t.Fatal("expected an error deleting with a stale old value")
	}
	if !errors.Is(err, ErrRefCASMismatch) {
		t.Errorf("expected ErrRefCASMismatch, got: %v", err)
	}
}

// TestGitEnvForcesLCAllC checks gitEnv's ordering guarantee: its own
// LC_ALL=C entry must appear before whatever the process environment
// contributes, since env lookup returns the first match - a caller-inherited
// LC_ALL must not silently win and reintroduce locale-dependent git output
// that isRefUpdateRetryable / syncapp's classifyPushRejection depend on.
func TestGitEnvForcesLCAllC(t *testing.T) {
	env := gitEnv()
	if len(env) == 0 || env[0] != "LC_ALL=C" {
		t.Fatalf("gitEnv()[0] = %q, want \"LC_ALL=C\" (must come first so it wins over any inherited LC_ALL)", env[0])
	}
}

func TestUpdateRefCAS(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	oid1 := commitEmpty(t, dir, "first")
	if err := r.UpdateRef(ctx, "refs/projects/task/abc", oid1, ZeroOID); err != nil {
		t.Fatal(err)
	}

	oid2 := commitEmpty(t, dir, "second")
	// Wrong old value must be rejected (simulates a losing concurrent writer).
	if err := r.UpdateRef(ctx, "refs/projects/task/abc", oid2, ZeroOID); err == nil {
		t.Error("expected CAS failure with wrong old value")
	}
	// Correct old value succeeds.
	if err := r.UpdateRef(ctx, "refs/projects/task/abc", oid2, oid1); err != nil {
		t.Fatal(err)
	}
	got, err := r.RevParse(ctx, "refs/projects/task/abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != oid2 {
		t.Errorf("got %q want %q", got, oid2)
	}
}

// TestParsePushPorcelainFastForward guards against a real regression: a
// successful fast-forward push's status flag is a literal space (' '), and
// naively TrimSpace-ing the whole porcelain line before reading the flag
// eats it, making the line look like it starts with the refspec text
// instead - which silently misreports every successful fast-forward push
// as a failure (caught via internal/app/syncapp's convergence test).
func TestParsePushPorcelainFastForward(t *testing.T) {
	out := "To /tmp/pushtest/origin\n" +
		" \trefs/projects/issue/xyz:refs/projects/issue/xyz\t62ba6ab..378c1ca\n" +
		"Done\n"
	results := parsePushPorcelain(out)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if !results[0].OK {
		t.Errorf("expected fast-forward push to parse as OK, got %+v", results[0])
	}
	if results[0].Refspec != "refs/projects/issue/xyz:refs/projects/issue/xyz" {
		t.Errorf("unexpected refspec: %q", results[0].Refspec)
	}
}

func TestParsePushPorcelainVariants(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
	}{
		{"new ref", "*\trefs/a:refs/a\t[new reference]", true},
		{"fast-forward", " \trefs/a:refs/a\tabc..def", true},
		{"forced", "+\trefs/a:refs/a\tabc...def (forced update)", true},
		{"up to date", "=\trefs/a:refs/a\t[up to date]", true},
		{"rejected", "!\trefs/a:refs/a\t[rejected] (non-fast-forward)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := "To origin\n" + c.line + "\nDone\n"
			results := parsePushPorcelain(out)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %+v", results)
			}
			if results[0].OK != c.ok {
				t.Errorf("got OK=%v, want %v (line %q)", results[0].OK, c.ok, c.line)
			}
		})
	}
}

func TestVerifyCommitUnsigned(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	oid := commitEmpty(t, dir, "unsigned commit")

	allowedSigners := filepath.Join(t.TempDir(), "allowed_signers")
	if err := os.WriteFile(allowedSigners, []byte("test@example.com ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(dir)
	result, err := r.VerifyCommit(context.Background(), oid, allowedSigners)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Errorf("expected an unsigned commit to fail verification, got Valid=true: %s", result.Output)
	}
}

func TestFetchAndPush(t *testing.T) {
	requireGit(t)
	origin := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	initRepo(t, clone)
	r := New(clone)
	ctx := context.Background()

	oid := commitEmpty(t, clone, "root")
	if err := r.UpdateRef(ctx, "refs/projects/issue/abc", oid, ZeroOID); err != nil {
		t.Fatal(err)
	}

	results, err := r.Push(ctx, origin, "refs/projects/issue/abc:refs/projects/issue/abc")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("expected successful push, got %+v", results)
	}

	// A second clone should be able to fetch it into the tracking namespace.
	clone2 := filepath.Join(t.TempDir(), "clone2")
	initRepo(t, clone2)
	r2 := New(clone2)
	if err := r2.Fetch(ctx, origin, "+refs/projects/*:refs/githive-remote/*"); err != nil {
		t.Fatal(err)
	}
	entries, err := r2.ForEachRef(ctx, "refs/githive-remote/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Ref != "refs/githive-remote/issue/abc" || entries[0].OID != oid {
		t.Fatalf("got %+v", entries)
	}
}

// commitFile writes path (relative to dir) with content, commits it, and
// returns the new commit hash. Used by the Show/Log tests.
func commitFile(t *testing.T, dir, path, content, msg string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", path)
	run("commit", "-m", msg)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(out))
}

func TestShow(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	oid := commitFile(t, dir, "Home.md", "# hello wiki\n", "add Home")
	if err := r.UpdateRef(ctx, "refs/projects/wiki/main", oid, ZeroOID); err != nil {
		t.Fatal(err)
	}

	got, err := r.Show(ctx, "refs/projects/wiki/main", "Home.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# hello wiki\n" {
		t.Errorf("Show content = %q", got)
	}

	if _, err := r.Show(ctx, "refs/projects/wiki/main", "Missing.md"); err == nil {
		t.Error("expected error showing a missing path")
	}
	if _, err := r.Show(ctx, "refs/projects/wiki/does-not-exist", "Home.md"); err == nil {
		t.Error("expected error showing under a missing ref")
	}
}

func TestLog(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	r := New(dir)
	ctx := context.Background()

	// Missing ref → empty, no error (a repo may have no wiki yet).
	entries, err := r.Log(ctx, "refs/projects/wiki/main", "")
	if err != nil {
		t.Fatalf("Log on missing ref: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty log for missing ref, got %+v", entries)
	}

	c1 := commitFile(t, dir, "Home.md", "one\n", "first")
	commitFile(t, dir, "design/sync.md", "sync\n", "second")
	if err := r.UpdateRef(ctx, "refs/projects/wiki/main", mustHead(t, dir), ZeroOID); err != nil {
		t.Fatal(err)
	}

	entries, err = r.Log(ctx, "refs/projects/wiki/main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 commits, got %d: %+v", len(entries), entries)
	}
	// Most-recent first.
	if entries[0].Subject != "second" || entries[1].Subject != "first" {
		t.Errorf("wrong order/subjects: %+v", entries)
	}
	if entries[1].Hash != c1 {
		t.Errorf("oldest hash = %q want %q", entries[1].Hash, c1)
	}
	if entries[0].Author != "test@example.com" {
		t.Errorf("author = %q", entries[0].Author)
	}
	if entries[0].Date == "" {
		t.Error("empty date")
	}

	// Path filter restricts to touching commits.
	only, err := r.Log(ctx, "refs/projects/wiki/main", "Home.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Subject != "first" {
		t.Fatalf("path-filtered log = %+v", only)
	}
}

func mustHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(out))
}
