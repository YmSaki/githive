package syncapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
)

// graftedRegistryOnOrigin reproduces #113's attack: a legitimate registry
// is shared through origin, then an attacker builds a registry from its own
// root and pushes a merge whose first parent is the legitimate head - a
// fast-forward for everyone else. It returns a victim clone that already
// holds the legitimate registry, and the legitimate root.
func graftedRegistryOnOrigin(t *testing.T) (victim, legitRoot string) {
	t.Helper()
	ctx := context.Background()
	origin := initBareOrigin(t)
	admin := cloneRepo(t, origin, "a@example.com")
	victim = cloneRepo(t, origin, "v@example.com")
	attacker := cloneRepo(t, origin, "evil@example.com")

	if err := usersapp.New(admin).AddUser(ctx, "alice", "", "a@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, admin, "origin", []string{registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, victim, "origin", []string{registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	legitHead := refOID(t, victim, registryRef)
	legitRoot = rootOID(t, victim, registryRef)

	// The attacker's own registry, created without fetching the real one.
	if err := usersapp.New(attacker).AddUser(ctx, "evil", "", "evil@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	evilHead := refOID(t, attacker, registryRef)
	if out, err := exec.Command("git", "-C", attacker, "fetch", "-q", "origin", registryRef+":refs/legit").CombinedOutput(); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	repo, err := chain.OpenRepository(attacker)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := chain.AppendMerge(repo, [2]plumbing.Hash{plumbing.NewHash(legitHead), plumbing.NewHash(evilHead)},
		map[string][]byte{"README": []byte("x\n")},
		chain.Signature{Name: "Evil", Email: "evil@example.com", When: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", attacker, "push", "-q", "origin", merged.String()+":"+registryRef).CombinedOutput(); err != nil {
		t.Fatalf("push graft: %v\n%s", err, out)
	}
	return victim, legitRoot
}

// TestSyncRefusesGraftedRegistryFastForward: the victim's sync sees a plain
// fast-forward, but the incoming history carries a second root, so it must
// be refused and the local ref left alone - with or without a pin.
func TestSyncRefusesGraftedRegistryFastForward(t *testing.T) {
	requireGit(t)
	for _, pinned := range []bool{false, true} {
		victim, legitRoot := graftedRegistryOnOrigin(t)
		if pinned {
			setConfig(t, victim, TrustRootPinKey, legitRoot)
		}
		before := refOID(t, victim, registryRef)
		_, err := Sync(context.Background(), victim, "origin", []string{registryRef}, 5)
		var conflict *TrustRootConflictError
		if !errors.As(err, &conflict) || !conflict.Grafted {
			t.Fatalf("pinned=%v: want grafted TrustRootConflictError, got %v", pinned, err)
		}
		if len(conflict.RemoteRoots) != 2 {
			t.Errorf("pinned=%v: want both roots reported, got %v", pinned, conflict.RemoteRoots)
		}
		if after := refOID(t, victim, registryRef); after != before {
			t.Errorf("pinned=%v: local registry moved %s -> %s", pinned, before, after)
		}
	}
}

// TestSyncRefusesGraftedRegistryOnFirstFetch: a clone with no local
// registry yet must not adopt a registry with two roots either.
func TestSyncRefusesGraftedRegistryOnFirstFetch(t *testing.T) {
	requireGit(t)
	victim, _ := graftedRegistryOnOrigin(t)
	if out, err := exec.Command("git", "-C", victim, "update-ref", "-d", registryRef).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	_, err := Sync(context.Background(), victim, "origin", []string{registryRef}, 5)
	var conflict *TrustRootConflictError
	if !errors.As(err, &conflict) || !conflict.Grafted {
		t.Fatalf("want grafted TrustRootConflictError, got %v", err)
	}
	if oid := refOID(t, victim, registryRef); oid != "" {
		t.Errorf("registry should not have been created, got %s", oid)
	}
}
