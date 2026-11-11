package verifyapp

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func corruptHeadEnvelope(t *testing.T, dir, ref string) {
	t.Helper()
	ctx := context.Background()
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := object.GetCommit(repo.Storer, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	c.ParentHashes = []plumbing.Hash{c.Hash}
	c.Message = "broken envelope\n\n{not json\n"
	c.PGPSignature = ""
	obj := repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		t.Fatal(err)
	}
	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, ref, hash.String(), oid); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAllKeepsOtherReportsWithMalformedEnvelope(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := issueapp.New(dir)
	good, err := svc.NewIssue(ctx, "good", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := svc.NewIssue(ctx, "bad", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	corruptHeadEnvelope(t, dir, "refs/projects/issue/"+bad)
	reports, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatalf("malformed event aborted verification: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("other report lost: %+v", reports)
	}
	goodSeen, badSeen := false, false
	for _, report := range reports {
		if report.Ref == "refs/projects/issue/"+good {
			goodSeen = report.CommitCount == 1
		}
		if report.Ref == "refs/projects/issue/"+bad {
			for _, issue := range report.Issues {
				badSeen = badSeen || issue.Code == "invalid_envelope"
			}
		}
	}
	if !goodSeen || !badSeen {
		t.Fatalf("partial reports incomplete: %+v", reports)
	}
}

func TestVerifyAllDoesNotTrustCorruptRegistryOrDropOtherReports(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	if err := usersapp.New(dir).AddUser(ctx, "tester", "", "tester@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	id, err := issueapp.New(dir).NewIssue(ctx, "good event", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	corruptHeadEnvelope(t, dir, refspace.UsersRegistryRef)
	reports, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatalf("corrupt registry aborted all reports: %v", err)
	}
	found := false
	for _, report := range reports {
		if report.Ref == "refs/projects/issue/"+id {
			for _, issue := range report.Issues {
				found = found || issue.Code == "invalid_registry"
			}
		}
	}
	if len(reports) != 2 || !found {
		t.Fatalf("registry not failclosed or report missing: %+v", reports)
	}
}

func TestVerifyAllReportsUnrecognizedRefWithoutFalseFailure(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	id, err := issueapp.New(dir).NewIssue(ctx, "known", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, "refs/projects/issue/"+id)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/future/item"
	if err := r.UpdateRef(ctx, ref, oid, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}
	reports, err := VerifyAll(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, report := range reports {
		if report.Ref == ref {
			found = report.OK() && len(report.Warnings) == 1 && report.Warnings[0].Code == "unrecognized_ref"
		}
	}
	if !found {
		t.Fatalf("unknown ref silently skipped or false failure: %+v", reports)
	}
}
