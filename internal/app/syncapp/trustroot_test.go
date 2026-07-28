package syncapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/gitx"
)

const registryRef = usersapp.RegistryRef

func setConfig(t *testing.T, dir, key, value string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "config", key, value).CombinedOutput(); err != nil {
		t.Fatalf("git config %s: %v\n%s", key, err, out)
	}
}

func refOID(t *testing.T, dir, ref string) string {
	t.Helper()
	oid, err := gitx.New(dir).RevParse(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func rootOID(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--max-parents=0", ref).Output()
	if err != nil {
		t.Fatalf("git rev-list --max-parents=0 %s: %v", ref, err)
	}
	roots := splitLines(string(out))
	if len(roots) != 1 {
		t.Fatalf("expected exactly 1 root commit on %s, got %v", ref, roots)
	}
	return roots[0]
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if line := s[start:]; line != "" {
		out = append(out, line)
	}
	return out
}

// twoIndependentRegistries builds the exact shape docs/03「共通祖先が無い場
// 合」describes for users/registry: two clones that each created their own
// registry with no shared commit, with A's already pushed so B sees it as
// the remote.
func twoIndependentRegistries(t *testing.T) (origin, dirA, dirB string) {
	t.Helper()
	ctx := context.Background()
	origin = initBareOrigin(t)
	dirA = cloneRepo(t, origin, "a@example.com")
	dirB = cloneRepo(t, origin, "b@example.com")

	if err := usersapp.New(dirA).AddUser(ctx, "alice", "Alice", "a@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{registryRef}, 5); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	// B creates its own registry without ever fetching A's.
	if err := usersapp.New(dirB).AddUser(ctx, "bob", "Bob", "b@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	return origin, dirA, dirB
}

// TestTrustRootConflictWithoutPinIsRefused is the headline scenario: with no
// pin, sync must not merge two identity ledgers, and must leave the local
// ref exactly where it was.
func TestTrustRootConflictWithoutPinIsRefused(t *testing.T) {
	requireGit(t)
	_, _, dirB := twoIndependentRegistries(t)
	ctx := context.Background()

	before := refOID(t, dirB, registryRef)
	_, err := Sync(ctx, dirB, "origin", []string{registryRef}, 5)
	if err == nil {
		t.Fatal("expected two unrelated registries to be refused")
	}
	var conflict *TrustRootConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected TrustRootConflictError, got %v", err)
	}
	if len(conflict.LocalRoots) != 1 || len(conflict.RemoteRoots) != 1 {
		t.Errorf("expected one root per side, got local=%v remote=%v", conflict.LocalRoots, conflict.RemoteRoots)
	}
	if conflict.LocalRoots[0] == conflict.RemoteRoots[0] {
		t.Error("the two roots should differ; the fixture did not produce unrelated registries")
	}
	if after := refOID(t, dirB, registryRef); after != before {
		t.Errorf("local ref moved despite the refusal: %s -> %s", before, after)
	}
}

// TestTrustRootPinKeepsLocal: the pin names B's own root, so B keeps its
// registry and does not merge the remote's in.
func TestTrustRootPinKeepsLocal(t *testing.T) {
	requireGit(t)
	_, _, dirB := twoIndependentRegistries(t)
	ctx := context.Background()

	before := refOID(t, dirB, registryRef)
	setConfig(t, dirB, TrustRootPinKey, rootOID(t, dirB, registryRef))

	results, err := Sync(ctx, dirB, "origin", []string{registryRef}, 5)
	if err != nil {
		t.Fatalf("sync with a local pin should succeed: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionKeptLocalTrustRoot {
		t.Fatalf("expected %s, got %+v", ActionKeptLocalTrustRoot, results)
	}
	if results[0].Warning == "" {
		t.Error("expected a warning explaining the remote registry was not merged")
	}
	if after := refOID(t, dirB, registryRef); after != before {
		t.Errorf("local ref moved: %s -> %s", before, after)
	}
}

// TestTrustRootPinAdoptsRemote: the pin names the remote's root, so B
// discards its own chain and takes the remote head.
func TestTrustRootPinAdoptsRemote(t *testing.T) {
	requireGit(t)
	_, dirA, dirB := twoIndependentRegistries(t)
	ctx := context.Background()

	remoteRoot := rootOID(t, dirA, registryRef)
	remoteHead := refOID(t, dirA, registryRef)
	setConfig(t, dirB, TrustRootPinKey, remoteRoot)

	results, err := Sync(ctx, dirB, "origin", []string{registryRef}, 5)
	if err != nil {
		t.Fatalf("sync with a remote pin should succeed: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionAdoptedRemoteTrustRoot {
		t.Fatalf("expected %s, got %+v", ActionAdoptedRemoteTrustRoot, results)
	}
	if got := refOID(t, dirB, registryRef); got != remoteHead {
		t.Errorf("local ref = %s, want the remote head %s", got, remoteHead)
	}
	if results[0].Warning == "" {
		t.Error("expected a warning noting the discarded local chain is in the reflog")
	}
}

// TestTrustRootPinMismatchIsRefused: a pin that matches neither side is as
// good as no pin.
func TestTrustRootPinMismatchIsRefused(t *testing.T) {
	requireGit(t)
	_, _, dirB := twoIndependentRegistries(t)
	setConfig(t, dirB, TrustRootPinKey, "0000000000000000000000000000000000000000")

	_, err := Sync(context.Background(), dirB, "origin", []string{registryRef}, 5)
	var conflict *TrustRootConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected TrustRootConflictError, got %v", err)
	}
	if conflict.Pin == "" {
		t.Error("expected the mismatching pin to be reported back")
	}
}

// TestUsersRegistryWithSharedHistoryStillMerges is the non-regression half:
// the refusal must be scoped to genuinely unrelated registries, not to every
// users divergence.
func TestUsersRegistryWithSharedHistoryStillMerges(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// One registry, shared by both clones.
	if err := usersapp.New(dirA).AddUser(ctx, "alice", "Alice", "a@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirB, "origin", []string{registryRef}, 5); err != nil {
		t.Fatal(err)
	}

	// Both add a user, diverging over shared history.
	if err := usersapp.New(dirA).AddUser(ctx, "carol", "Carol", "c@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if err := usersapp.New(dirB).AddUser(ctx, "dave", "Dave", "d@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, dirA, "origin", []string{registryRef}, 5); err != nil {
		t.Fatal(err)
	}
	results, err := Sync(ctx, dirB, "origin", []string{registryRef}, 5)
	if err != nil {
		t.Fatalf("an ordinary users divergence must still merge: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionMerged {
		t.Fatalf("expected %s, got %+v", ActionMerged, results)
	}

	users, _, _, err := usersapp.New(dirB).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, u := range users {
		if name, ok := u["username"].(string); ok {
			names[name] = true
		}
	}
	for _, want := range []string{"alice", "carol", "dave"} {
		if !names[want] {
			t.Errorf("expected %s to survive the event-union merge, got %v", want, names)
		}
	}
}

// TestNonUsersFeatureWithTwoRootsStillMerges keeps the special case scoped
// to users: docs/03 says every other feature's two-root case merges as usual.
func TestNonUsersFeatureWithTwoRootsStillMerges(t *testing.T) {
	requireGit(t)
	origin := initBareOrigin(t)
	dirA := cloneRepo(t, origin, "a@example.com")
	dirB := cloneRepo(t, origin, "b@example.com")
	ctx := context.Background()

	// Two clones independently create an issue, then force both onto the
	// same ref name so the ref has two unrelated roots.
	idA, err := issueapp.New(dirA).NewIssue(ctx, "from a", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := issueapp.New(dirB).NewIssue(ctx, "from b", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	refA := "refs/projects/issue/" + idA
	refB := "refs/projects/issue/" + idB
	if _, err := Sync(ctx, dirA, "origin", []string{refA}, 5); err != nil {
		t.Fatal(err)
	}
	// Point B's local chain at A's ref name, giving that ref two roots.
	if out, err := exec.Command("git", "-C", dirB, "update-ref", refA, refOID(t, dirB, refB)).CombinedOutput(); err != nil {
		t.Fatalf("git update-ref: %v\n%s", err, out)
	}

	results, err := Sync(ctx, dirB, "origin", []string{refA}, 5)
	if err != nil {
		t.Fatalf("a non-users two-root case must merge automatically: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionMerged {
		t.Fatalf("expected %s, got %+v", ActionMerged, results)
	}
}
