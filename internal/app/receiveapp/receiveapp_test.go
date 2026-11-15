package receiveapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}

const zero = "0000000000000000000000000000000000000000"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// history builds base <- child and an unrelated sibling of child (both on
// top of base), returning their OIDs.
func history(t *testing.T) (dir, base, child, sibling string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q")
	base = git(t, dir, "commit-tree", "-m", "base", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	child = git(t, dir, "commit-tree", "-p", base, "-m", "child", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	sibling = git(t, dir, "commit-tree", "-p", base, "-m", "sibling", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	return dir, base, child, sibling
}

func codes(rs []Rejection) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Ref+"="+r.Code)
	}
	return out
}

func TestParseUpdates(t *testing.T) {
	in := zero + " " + strings.Repeat("a", 40) + " refs/heads/main\n\n" +
		strings.Repeat("b", 64) + " " + strings.Repeat("c", 64) + " refs/projects/issue/x\n"
	got, err := ParseUpdates(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].IsCreate() || got[0].IsDelete() || got[1].Ref != "refs/projects/issue/x" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseUpdatesRejectsMalformed(t *testing.T) {
	a := strings.Repeat("a", 40)
	for _, in := range []string{
		a + " " + a,                                         // フィールド不足
		a + " " + a + " refs/heads/x extra",                 // フィールド過多
		"xyz " + a + " refs/heads/x",                        // 不正な OID
		a + " " + strings.Repeat("a", 64) + " refs/heads/x", // 長さ不一致
		a + " " + a + " HEAD",                               // refs/ 外
		a + " " + a + " --upload-pack=x",                    // オプション風の ref
	} {
		if _, err := ParseUpdates(strings.NewReader(in + "\n")); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ParseUpdates(%q) err = %v, want ErrInvalidInput", in, err)
		}
	}
}

func TestCheckFastForwardOnlyUnderProjects(t *testing.T) {
	dir, base, child, sibling := history(t)
	s := New(dir)
	got, err := s.Check(context.Background(), Config{}, []RefUpdate{
		{Old: base, New: child, Ref: "refs/projects/issue/ok"},       // fast-forward
		{Old: child, New: sibling, Ref: "refs/projects/issue/force"}, // 非 fast-forward
		{Old: child, New: base, Ref: "refs/projects/task/rewind"},    // 巻き戻し
		{Old: zero, New: sibling, Ref: "refs/projects/issue/new"},    // 作成
		{Old: child, New: zero, Ref: "refs/projects/issue/del"},      // 削除（ADR-0035 決定 3）
		{Old: child, New: sibling, Ref: "refs/heads/main"},           // projects 外の force push
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"refs/projects/issue/force=non_fast_forward", "refs/projects/task/rewind=non_fast_forward"}
	if strings.Join(codes(got), ",") != strings.Join(want, ",") {
		t.Fatalf("rejections = %v, want %v", codes(got), want)
	}
}

func TestCheckReservedNamespaceAlwaysRejected(t *testing.T) {
	dir, base, child, _ := history(t)
	s := New(dir)
	for _, cfg := range []Config{{}, {Mode: NamespaceStrict}} {
		got, err := s.Check(context.Background(), cfg, []RefUpdate{
			{Old: zero, New: child, Ref: "refs/githive-remote/origin/issue/x"},
			{Old: base, New: child, Ref: "refs/githive-remote"},
			{Old: zero, New: child, Ref: "refs/githive-remotex/ok-in-open"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var reserved int
		for _, r := range got {
			if r.Code == CodeReservedNamespace {
				reserved++
			}
		}
		if reserved != 2 {
			t.Fatalf("mode %q: rejections = %v, want 2 reserved_namespace", cfg.Mode, codes(got))
		}
	}
}

func TestCheckStrictNamespaces(t *testing.T) {
	dir, _, child, _ := history(t)
	s := New(dir)
	updates := []RefUpdate{
		{Old: zero, New: child, Ref: "refs/heads/main"},
		{Old: zero, New: child, Ref: "refs/tags/v1"},
		{Old: zero, New: child, Ref: "refs/projects/issue/a"},
		{Old: zero, New: child, Ref: "refs/notes/commits"},
		{Old: zero, New: child, Ref: "refs/pull/1/head"},
	}
	open, err := s.Check(context.Background(), Config{}, updates)
	if err != nil || len(open) != 0 {
		t.Fatalf("open mode: %v %v, want no rejections", codes(open), err)
	}
	strict, err := s.Check(context.Background(), Config{Mode: NamespaceStrict, AllowedNamespaces: []string{"refs/notes"}}, updates)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(codes(strict), ","); got != "refs/pull/1/head=namespace_not_allowed" {
		t.Fatalf("strict mode rejections = %s", got)
	}
}

func TestValidateConfig(t *testing.T) {
	for _, cfg := range []Config{
		{Mode: "closed"},
		{Mode: NamespaceStrict, AllowedNamespaces: []string{"notes/"}},
		{Mode: NamespaceStrict, AllowedNamespaces: []string{"refs/"}},
		{Mode: NamespaceStrict, AllowedNamespaces: []string{"refs/githive-remote/origin"}},
		{Mode: NamespaceStrict, AllowedNamespaces: []string{"refs/githive-remote"}},
	} {
		if err := ValidateConfig(cfg); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ValidateConfig(%+v) = %v, want ErrInvalidInput", cfg, err)
		}
	}
	if err := ValidateConfig(Config{Mode: NamespaceStrict, AllowedNamespaces: []string{"refs/notes"}}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestCheckGitFailureIsAnError(t *testing.T) {
	dir, _, child, _ := history(t)
	missing := strings.Repeat("d", 40)
	_, err := New(dir).Check(context.Background(), Config{}, []RefUpdate{
		{Old: missing, New: child, Ref: "refs/projects/issue/x"},
	})
	if err == nil {
		t.Fatal("Check with an unknown old object succeeded; want an error so the hook refuses the push")
	}
}
