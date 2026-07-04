package gitx

import (
	"bytes"
	"context"
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
