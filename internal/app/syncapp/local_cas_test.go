package syncapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	wrapper := filepath.Join(bin, "git")
	for suffix, value := range map[string]string{
		".realgit": realGit, ".repo": dir, ".ref": ref,
		".contenders": strings.Join(contenders, "\n") + "\n",
	} {
		if err := os.WriteFile(wrapper+suffix, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const script = `#!/bin/sh
set -eu
realgit=$(cat "$0.realgit")
ref=$(cat "$0.ref")
if [ "${1-}" = -C ] && [ "${3-}" = update-ref ] && [ "${4-}" = "$ref" ]; then
  count=0
  if [ -f "$0.count" ]; then count=$(cat "$0.count"); fi
  next_count=$((count + 1))
  contender=$(sed -n "${next_count}p" "$0.contenders")
  if [ -n "$contender" ]; then
    repo=$(cat "$0.repo")
    if old=$("$realgit" -C "$repo" rev-parse --verify "$ref" 2>/dev/null); then :; else old=0000000000000000000000000000000000000000; fi
    "$realgit" -C "$repo" update-ref "$ref" "$contender" "$old"
    printf '%s' "$next_count" > "$0.count"
  fi
fi
exec "$realgit" "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return wrapper + ".count"
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
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
			unlock := chain.LockRepository(b)
			entered := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				close(entered)
				if operation == "append" {
					finished <- issueapp.New(b).Label(ctx, id, []string{"local"}, nil)
				} else {
					finished <- mergeAndAdvance(ctx, b, ref, refspace.FeatureIssue, local, remote)
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
			case <-ctx.Done():
				t.Fatalf("%s did not resume after releasing repository lock", operation)
			}
		})
	}
}
