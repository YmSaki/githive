package initapp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
)

// The tracking namespace is shared across remotes. Init must adopt only refs
// that still exist on the remote selected for this invocation.
func TestInitPrunesStaleTrackingBeforeAdoption(t *testing.T) {
	for _, changeRemote := range []bool{false, true} {
		name := "deleted-upstream"
		if changeRemote {
			name = "different-remote"
		}
		t.Run(name, func(t *testing.T) {
			dir := newTestRepo(t)
			ctx := context.Background()
			r := gitx.New(dir)
			if _, err := Init(ctx, dir, "origin", "old-project"); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) {
				t.Helper()
				if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			git("push", "origin", "refs/projects/meta/config")
			git("fetch", "origin", trackingRefspec)
			remote := "origin"
			if changeRemote {
				other := filepath.Join(t.TempDir(), "other.git")
				git("init", "--bare", other)
				git("remote", "add", "other", other)
				remote = "other"
			} else {
				git("push", "origin", ":refs/projects/meta/config")
			}
			git("update-ref", "-d", "refs/projects/meta/config")
			result, err := Init(ctx, dir, remote, "new-project")
			if err != nil {
				t.Fatal(err)
			}
			if !result.MetaConfigMade || result.Remote.HasMetaConfig || len(result.Remote.AdoptedRefs) != 0 {
				t.Fatalf("empty remote incorrectly adopted stale config: %+v", result)
			}
			oid, err := r.RevParse(ctx, "refs/githive-remote/meta/config")
			if err != nil {
				t.Fatal(err)
			}
			if oid != "" {
				t.Fatalf("stale tracking ref survived fetch: %s", oid)
			}
		})
	}
}
