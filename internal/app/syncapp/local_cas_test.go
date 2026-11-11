package syncapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/refspace"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// injectLocalCASContenders wraps the real git command and advances the local
// ref from a separate process immediately before sync's actual update-ref CAS.
// Every contender is a prebuilt valid event commit; no production test seam or
// mocked CAS result is involved.
func injectLocalCASContenders(t *testing.T, dir, ref string, contenders []string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	wrapper := filepath.Join(bin, name)
	for suffix, value := range map[string]string{
		".realgit": realGit, ".repo": dir, ".ref": ref,
		".contenders": strings.Join(contenders, "\n") + "\n",
	} {
		if err := os.WriteFile(wrapper+suffix, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Re-execute a copy of this native test binary. Windows LookPath does
	// not select extensionless POSIX scripts ahead of git.exe.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, native, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localCASGitHelperEnv, wrapper)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	selected, err := exec.LookPath("git")
	samePath := selected == wrapper
	if runtime.GOOS == "windows" {
		samePath = strings.EqualFold(filepath.Clean(selected), filepath.Clean(wrapper))
	}
	if err != nil || !samePath {
		t.Fatalf("native CAS helper was not selected: got %q want %q (%v)", selected, wrapper, err)
	}
	return wrapper + ".count"
}

const localCASGitHelperEnv = "GITHIVE_TEST_LOCAL_CAS_GIT_HELPER"

// runLocalCASGitHelper runs before testing parses Git's arguments. All
// operations still use the real Git executable in a separate process.
func runLocalCASGitHelper(prefix string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "local CAS git helper:", err)
		return 1
	}
	read := func(suffix string) (string, error) {
		value, err := os.ReadFile(prefix + suffix)
		return strings.TrimSpace(string(value)), err
	}
	realGit, err := read(".realgit")
	if err != nil {
		return fail(err)
	}
	ref, err := read(".ref")
	if err != nil {
		return fail(err)
	}
	args := os.Args[1:]
	if len(args) >= 4 && args[0] == "-C" && args[2] == "update-ref" && args[3] == ref {
		count := 0
		if raw, err := read(".count"); err == nil {
			count, err = strconv.Atoi(raw)
			if err != nil {
				return fail(err)
			}
		} else if !os.IsNotExist(err) {
			return fail(err)
		}
		raw, err := read(".contenders")
		if err != nil {
			return fail(err)
		}
		contenders := strings.Split(raw, "\n")
		if count < len(contenders) && contenders[count] != "" {
			dir, err := read(".repo")
			if err != nil {
				return fail(err)
			}
			old := gitx.ZeroOID
			if out, err := exec.Command(realGit, "-C", dir, "rev-parse", "--verify", ref).Output(); err == nil {
				old = strings.TrimSpace(string(out))
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 128 {
				return fail(err)
			}
			if out, err := exec.Command(realGit, "-C", dir, "update-ref", ref, contenders[count], old).CombinedOutput(); err != nil {
				return fail(fmt.Errorf("inject contender: %w: %s", err, out))
			}
			if err := os.WriteFile(prefix+".count", []byte(strconv.Itoa(count+1)), 0o600); err != nil {
				return fail(err)
			}
		}
	}
	command := exec.Command(realGit, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return fail(err)
	}
	return 0
}

func TestSyncReplansLocalCASWithoutDroppingConcurrentEvents(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diverged   bool
		missing    bool
		contenders int
		retries    int
		exhausted  bool
	}{
		{"adopt-remote", false, true, 1, 1, false},
		{"fast-forward", false, false, 1, 1, false},
		{"merge", true, false, 1, 1, false},
		{"bounded-exhaustion", true, false, 2, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			a := cloneRepo(t, origin, "a@example.com")
			b := cloneRepo(t, origin, "b@example.com")
			refs := seedIssues(t, a, 2)
			if _, err := Sync(ctx, a, "origin", refs, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := Sync(ctx, b, "origin", refs, 0); err != nil {
				t.Fatal(err)
			}
			ref := refs[1]
			id := strings.TrimPrefix(ref, "refs/projects/issue/")
			if err := issueapp.New(a).Label(ctx, id, []string{"remote"}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Sync(ctx, a, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			svc := issueapp.New(b)
			if tc.diverged {
				if err := svc.Label(ctx, id, []string{"local"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			r := gitx.New(b)
			old, err := r.RevParse(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			var contenders []string
			for i := 0; i < tc.contenders; i++ {
				if err := svc.Label(ctx, id, []string{fmt.Sprintf("concurrent-%d", i)}, nil); err != nil {
					t.Fatal(err)
				}
				head, err := r.RevParse(ctx, ref)
				if err != nil {
					t.Fatal(err)
				}
				contenders = append(contenders, head)
			}
			if tc.missing {
				if err := r.DeleteRef(ctx, ref, contenders[len(contenders)-1]); err != nil {
					t.Fatal(err)
				}
			} else if err := r.UpdateRef(ctx, ref, old, contenders[len(contenders)-1]); err != nil {
				t.Fatal(err)
			}
			countPath := injectLocalCASContenders(t, b, ref, contenders)
			results, err := Sync(ctx, b, "origin", refs, tc.retries)
			if tc.exhausted {
				if !errors.Is(err, ErrRetriesExhausted) || errors.Is(err, gitx.ErrRefCASMismatch) {
					t.Fatalf("want bounded retry exhaustion, got results=%+v err=%v", results, err)
				}
				if len(results) != 1 || results[0].Ref != refs[0] {
					t.Fatalf("completed ref result lost: %+v", results)
				}
			} else if err != nil || len(results) != 2 {
				t.Fatalf("sync must replan local CAS loss: results=%+v err=%v", results, err)
			}
			count, err := os.ReadFile(countPath)
			if err != nil || string(count) != fmt.Sprint(tc.contenders) {
				t.Fatalf("injected %q contenders, want %d: %v", count, tc.contenders, err)
			}
			state, err := svc.Show(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			labels := fmt.Sprint(state.Meta["labels"])
			for i := 0; i < tc.contenders; i++ {
				if !strings.Contains(labels, fmt.Sprintf("concurrent-%d", i)) {
					t.Fatalf("concurrent event lost: %s", labels)
				}
			}
			if !tc.exhausted && !strings.Contains(labels, "remote") {
				t.Fatalf("remote event lost: %s", labels)
			}
			if tc.diverged && !strings.Contains(labels, "local") {
				t.Fatalf("original local event lost: %s", labels)
			}
			final, err := r.RevParse(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if ancestor, err := r.IsAncestor(ctx, contenders[len(contenders)-1], final); err != nil || !ancestor {
				t.Fatalf("concurrent commit history lost: %v %v", ancestor, err)
			}
			if !tc.exhausted {
				remote, err := exec.Command("git", "-C", origin, "rev-parse", ref).Output()
				if err != nil || strings.TrimSpace(string(remote)) != final {
					t.Fatalf("merged state not published: remote=%q local=%q err=%v", remote, final, err)
				}
			}
		})
	}
}

func TestLocalMergeAndAppendShareRepositoryLock(t *testing.T) {
	for _, operation := range []string{"append", "merge"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			a := cloneRepo(t, origin, "a@example.com")
			b := cloneRepo(t, origin, "b@example.com")
			ref := seedIssues(t, a, 1)[0]
			if _, err := Sync(ctx, a, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := Sync(ctx, b, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			id := strings.TrimPrefix(ref, "refs/projects/issue/")
			if err := issueapp.New(a).Label(ctx, id, []string{"remote"}, nil); err != nil {
				t.Fatal(err)
			}
			local, err := gitx.New(b).RevParse(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			remote, err := gitx.New(a).RevParse(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := gitx.New(b).Fetch(ctx, a, "+"+ref+":refs/githive-remote/issue/"+id); err != nil {
				t.Fatal(err)
			}
			// Fixture Git processes are slower on Windows CI. Bound the
			// operation under test independently of clone/seed/fetch setup.
			operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			unlock := chain.LockRepository(b)
			entered := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				close(entered)
				if operation == "append" {
					finished <- issueapp.New(b).Label(operationCtx, id, []string{"local"}, nil)
				} else {
					finished <- mergeAndAdvance(operationCtx, b, ref, refspace.FeatureIssue, local, remote)
				}
			}()
			<-entered
			select {
			case err := <-finished:
				unlock()
				t.Fatalf("%s bypassed the shared repository lock: %v", operation, err)
			case <-time.After(100 * time.Millisecond):
			}
			unlock()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-operationCtx.Done():
				t.Fatalf("%s did not resume after releasing repository lock: %v", operation, operationCtx.Err())
			}
		})
	}
}
