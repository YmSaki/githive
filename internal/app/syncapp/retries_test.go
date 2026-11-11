package syncapp

import (
	"context"
	"errors"
	"fmt"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each pre-push hook advances the remote after fetch but before push. The
// remote commits repeat the seed event, so the next round can still fold a
// valid chain; their new parent guarantees a distinct commit every round.
func TestSyncRetryBudgetCountsRetriesAfterInitialAttempt(t *testing.T) {
	requireGit(t)
	for _, retries := range []int{0, 1, 5} {
		t.Run(fmt.Sprint(retries), func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			dir := cloneRepo(t, origin, "a@example.com")
			ref := seedIssues(t, dir, 1)[0]
			if _, err := Sync(ctx, dir, "origin", []string{ref}, 0); err != nil {
				t.Fatal(err)
			}
			message, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%B", ref).Output()
			if err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string][]byte{
				"race-origin": []byte(filepath.ToSlash(origin)), "race-ref": []byte(ref), "race-message": message,
			} {
				if err := os.WriteFile(filepath.Join(dir, ".git", name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			script := `#!/bin/sh
set -eu
origin=$(cat .git/race-origin)
ref=$(cat .git/race-ref)
old=$(git -C "$origin" rev-parse "$ref")
tree=$(git -C "$origin" rev-parse "$old^{tree}")
new=$(git -C "$origin" -c user.name=Race -c user.email=race@example.com commit-tree "$tree" -p "$old" < .git/race-message)
git -C "$origin" update-ref "$ref" "$new" "$old"
`
			if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-push"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			// A local edit ensures the first attempt also needs a push.
			if err := issueapp.New(dir).Label(ctx, strings.TrimPrefix(ref, "refs/projects/issue/"), []string{"local"}, nil); err != nil {
				t.Fatal(err)
			}
			trace := newGitTrace(t)
			trace.reset()
			_, err = Sync(ctx, dir, "origin", []string{ref}, retries)
			if !errors.Is(err, ErrRetriesExhausted) {
				t.Fatalf("got %v, want ErrRetriesExhausted", err)
			}
			if got := len(trace.pushes()); got != retries+1 {
				t.Fatalf("got %d pushes, want %d", got, retries+1)
			}
		})
	}
}
