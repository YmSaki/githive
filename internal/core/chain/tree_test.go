package chain

import (
	"os/exec"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
)

func TestWriteTreeRejectsInvalidPathsBeforeWritingObjects(t *testing.T) {
	for _, path := range []string{"", "/a", "a/", "a//b", ".", "..", "a/./b", "a/../b", ".git/config", "a/.GIT/config", "a\x00b", `a\b`} {
		t.Run(path, func(t *testing.T) {
			storage := memory.NewStorage()
			repo, err := git.Init(storage, nil)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := WriteTree(repo, map[string][]byte{"good.txt": []byte("good"), path: []byte("bad")})
			if err == nil || hash != plumbing.ZeroHash {
				t.Fatalf("invalid path accepted: hash=%s err=%v", hash, err)
			}
			if len(storage.Objects) != 0 {
				t.Fatalf("validation wrote %d objects", len(storage.Objects))
			}
		})
	}
}

func TestWriteTreeRejectsFileDirectoryCollisionsDeterministically(t *testing.T) {
	storage := memory.NewStorage()
	repo, err := git.Init(storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"a": []byte("file"), "a/b": []byte("child"), "z": []byte("other"), "z/b": []byte("child")}
	var firstError string
	for i := 0; i < 100; i++ {
		hash, err := WriteTree(repo, files)
		if err == nil || hash != plumbing.ZeroHash {
			t.Fatalf("collision accepted: hash=%s err=%v", hash, err)
		}
		if i == 0 {
			firstError = err.Error()
		} else if err.Error() != firstError {
			t.Fatalf("non-deterministic rejection: %q vs %q", firstError, err)
		}
	}
	if len(storage.Objects) != 0 {
		t.Fatalf("collision wrote %d objects", len(storage.Objects))
	}
}

func TestWriteTreeMatchesGitTreeOrdering(t *testing.T) {
	requireGit(t)
	dir := initBareDir(t)
	repo, err := OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitCommand := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	blob := gitCommand("content", "hash-object", "-w", "--stdin")
	subtree := gitCommand("100644 blob "+blob+"\tz.md\n", "mktree")
	wiki := gitCommand("100644 blob "+blob+"\tHome.md\n", "mktree")
	expected := gitCommand("040000 tree "+wiki+"\t.wiki\n100644 blob "+blob+"\ta.md\n040000 tree "+subtree+"\ta\n100644 blob "+blob+"\ta0.md\n", "mktree")
	files := map[string][]byte{"a.md": []byte("content"), "a/z.md": []byte("content"), "a0.md": []byte("content"), ".wiki/Home.md": []byte("content")}
	for i := 0; i < 20; i++ {
		actual, err := WriteTree(repo, files)
		if err != nil {
			t.Fatal(err)
		}
		if actual.String() != expected {
			t.Fatalf("tree differs from git mktree: got %s want %s", actual, expected)
		}
	}
	gitCommand("", "fsck", "--strict", "--no-reflogs", expected)
}
