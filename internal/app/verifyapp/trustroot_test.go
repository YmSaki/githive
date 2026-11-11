package verifyapp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// buildDoubleRootRegistry makes refs/projects/users/registry a merge of two
// registries with unrelated roots (first parent X, second parent Y): the
// shape of #113's graft attack. It returns the two root OIDs.
func buildDoubleRootRegistry(t *testing.T) (dir, rootX, rootY string) {
	t.Helper()
	dir = newTestRepo(t)
	ctx := context.Background()
	svc := usersapp.New(dir)
	if err := svc.AddUser(ctx, "alice", "", "tester@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	headX := revParse(t, dir, refspace.UsersRegistryRef)
	if out, err := exec.Command("git", "-C", dir, "update-ref", "-d", refspace.UsersRegistryRef).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := svc.AddUser(ctx, "evil", "", "tester@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	headY := revParse(t, dir, refspace.UsersRegistryRef)

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := chain.AppendMerge(repo, [2]plumbing.Hash{plumbing.NewHash(headX), plumbing.NewHash(headY)},
		map[string][]byte{"README": []byte("x\n")},
		chain.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dir, refspace.UsersRegistryRef, merged, headY); err != nil {
		t.Fatal(err)
	}
	return dir, headX, headY // single-commit chains: head == root
}

func codesFor(report *RefReport, code string) []string {
	var hashes []string
	for _, i := range report.Issues {
		if i.Code == code {
			hashes = append(hashes, i.CommitHash)
		}
	}
	return hashes
}

// TestVerifyRefDoubleRootAlwaysReported is #113's verify half: a registry
// with two unrelated roots must be reported whatever the pin says, and with
// a pin, the non-pinned root is the one reported.
func TestVerifyRefDoubleRootAlwaysReported(t *testing.T) {
	requireGit(t)
	dir, rootX, rootY := buildDoubleRootRegistry(t)
	ctx := context.Background()

	for _, c := range []struct {
		name, pin string
		want      []string
	}{
		{"no pin", "", sortedPair(rootX, rootY)},
		{"pin=X", rootX, []string{rootY}},
		{"pin=Y", rootY, []string{rootX}},
	} {
		report, err := VerifyRef(ctx, dir, refspace.UsersRegistryRef, c.pin)
		if err != nil {
			t.Fatal(err)
		}
		if got := codesFor(report, "multiple_trust_roots"); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: multiple_trust_roots on %v, want %v (issues %+v)", c.name, got, c.want, report.Issues)
		}
		if c.pin != "" && report.TrustRootHash != c.pin {
			t.Errorf("%s: TrustRootHash = %s, want the pin", c.name, report.TrustRootHash)
		}
	}
}

// TestVerifyRefNonTrustedRootIsNotAdmin: a grafted root's first commit is
// checked against an empty registry, so its self-made admin is reported
// under rule 4 too; the pinned root is exempt.
func TestVerifyRefNonTrustedRootIsNotAdmin(t *testing.T) {
	requireGit(t)
	dir, rootX, rootY := buildDoubleRootRegistry(t)
	report, err := VerifyRef(context.Background(), dir, refspace.UsersRegistryRef, rootX)
	if err != nil {
		t.Fatal(err)
	}
	if got := codesFor(report, "registry_change_without_admin"); len(got) != 1 || got[0] != rootY {
		t.Fatalf("want only the grafted root %s flagged, got %v", rootY, got)
	}
}

func TestVerifyRefSingleRootNoMultipleRootIssue(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	if err := usersapp.New(dir).AddUser(ctx, "alice", "", "tester@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := usersapp.New(dir).AddUser(ctx, "bob", "", "bob@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyRef(ctx, dir, refspace.UsersRegistryRef, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(codesFor(report, "multiple_trust_roots")) + len(codesFor(report, "registry_change_without_admin")); n != 0 {
		t.Fatalf("unexpected issues: %+v", report.Issues)
	}
}

func sortedPair(a, b string) []string {
	if a < b {
		return []string{a, b}
	}
	return []string{b, a}
}
