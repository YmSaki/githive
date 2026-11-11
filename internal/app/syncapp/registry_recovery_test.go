package syncapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/gitx"
)

func registryRecoveryFixture(t *testing.T) (origin, dir, local, remote string) {
	t.Helper()
	origin, a, b := twoIndependentRegistries(t)
	ctx := context.Background()
	r := gitx.New(b)
	if err := r.Fetch(ctx, "origin", "+refs/projects/*:refs/githive-remote/*"); err != nil {
		t.Fatal(err)
	}
	remote = refOID(t, a, registryRef)
	setConfig(t, b, TrustRootPinKey, rootOID(t, a, registryRef))
	return origin, b, refOID(t, b, registryRef), remote
}

func recoveryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRegistryRecoverySurvivesNoInitReflogExpiryAndGC(t *testing.T) {
	origin, dir, _, remote := registryRecoveryFixture(t)
	ctx := context.Background()
	recoveryGit(t, dir, "config", "--unset", "core.logAllRefUpdates")
	if out, err := exec.Command("git", "-C", dir, "config", "--local", "--get", "core.logAllRefUpdates").CombinedOutput(); err == nil {
		t.Fatalf("fixture unexpectedly initialized reflogs: %s", out)
	}
	if err := usersapp.New(dir).AddUser(ctx, "carol", "", "carol@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	local := refOID(t, dir, registryRef)
	history := recoveryGit(t, dir, "rev-list", local)
	if out := recoveryGit(t, dir, "reflog", "show", "--format=%H", registryRef); out != "" {
		t.Fatalf("fixture must have empty reflog: %s", out)
	}
	results, err := Sync(ctx, dir, "origin", []string{registryRef}, 0)
	if err != nil || len(results) != 1 {
		t.Fatalf("adoption: %v %v", results, err)
	}
	result := results[0]
	if result.BackupRef != "refs/githive-recovery/users/"+local || result.LocalOID != local || result.RemoteOID != remote {
		t.Fatalf("incomplete recovery receipt: %+v", result)
	}
	recoveryGit(t, dir, "reflog", "expire", "--expire=now", "--all")
	recoveryGit(t, dir, "gc", "--prune=now")
	if refOID(t, dir, result.BackupRef) != local || recoveryGit(t, dir, "rev-list", result.BackupRef) != history {
		t.Fatal("GC erased the old tip or its history")
	}
	if !strings.Contains(recoveryGit(t, dir, "show", result.BackupRef+":users/carol.json"), "carol@example.com") {
		t.Fatal("GC erased materialized old state")
	}
	if refOID(t, origin, result.BackupRef) != "" {
		t.Fatal("local recovery ref was automatically published")
	}
	if refOID(t, dir, registryRef) != remote {
		t.Fatal("pin resolution changed")
	}
}

func TestRegistryRecoveryIsIdempotentAndRefusesOverwriteOrFailedSave(t *testing.T) {
	for _, mode := range []string{"same", "different", "locked"} {
		t.Run(mode, func(t *testing.T) {
			_, dir, local, remote := registryRecoveryFixture(t)
			ctx := context.Background()
			r := gitx.New(dir)
			ref := "refs/githive-recovery/users/" + local
			switch mode {
			case "same":
				if _, err := preserveRegistryTip(ctx, r, local); err != nil {
					t.Fatal(err)
				}
			case "different":
				if err := r.UpdateRef(ctx, ref, remote, gitx.ZeroOID); err != nil {
					t.Fatal(err)
				}
			case "locked":
				path := filepath.Join(dir, ".git", filepath.FromSlash(ref)+".lock")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("held"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, handled, err := resolveTrustRoots(ctx, dir, r, registryRef, local, remote)
			if mode == "same" {
				if err != nil || !handled || result.BackupRef != ref || refOID(t, dir, registryRef) != remote {
					t.Fatalf("idempotent backup failed: %+v %v", result, err)
				}
				return
			}
			if err == nil || handled {
				t.Fatalf("failed preservation adopted remote: %+v %v", result, err)
			}
			if refOID(t, dir, registryRef) != local {
				t.Fatal("failed backup changed current registry")
			}
			if mode == "different" && refOID(t, dir, ref) != remote {
				t.Fatal("existing recovery ref was overwritten")
			}
		})
	}
}

func TestRegistryRecoveryPreservesContenderWhenAdoptionLosesCAS(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "one-attempt", true: "replan"}[retry], func(t *testing.T) {
			_, dir, local, remote := registryRecoveryFixture(t)
			ctx := context.Background()
			r := gitx.New(dir)
			if err := usersapp.New(dir).AddUser(ctx, "carol", "", "carol@example.com", "human", nil); err != nil {
				t.Fatal(err)
			}
			contender := refOID(t, dir, registryRef)
			if err := r.UpdateRef(ctx, registryRef, local, contender); err != nil {
				t.Fatal(err)
			}
			injectLocalCASContenders(t, dir, registryRef, []string{contender})
			if retry {
				results, err := Sync(ctx, dir, "origin", []string{registryRef}, 1)
				if err != nil || len(results) != 1 || results[0].LocalOID != contender {
					t.Fatalf("replan lost new local receipt: %+v %v", results, err)
				}
				if refOID(t, dir, registryRef) != remote || refOID(t, dir, "refs/githive-recovery/users/"+contender) != contender {
					t.Fatal("replanned adoption erased concurrent head")
				}
			} else {
				_, _, err := resolveTrustRoots(ctx, dir, r, registryRef, local, remote)
				if !errors.Is(err, gitx.ErrRefCASMismatch) {
					t.Fatalf("adoption ignored lost CAS: %v", err)
				}
				if refOID(t, dir, registryRef) != contender {
					t.Fatal("adoption overwrote concurrent head")
				}
			}
			if refOID(t, dir, "refs/githive-recovery/users/"+local) != local {
				t.Fatal("lost CAS erased original backup")
			}
		})
	}
}
