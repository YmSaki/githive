package synthrepo

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestGenerateBoundaryCases(t *testing.T) {
	t.Run("zero_issues", func(t *testing.T) {
		dir := t.TempDir()
		res, err := Generate(dir, Config{Issues: 0})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.IssueRefs) != 0 {
			t.Fatalf("got %d refs, want 0", len(res.IssueRefs))
		}
	})

	t.Run("one_issue_zero_comments", func(t *testing.T) {
		dir := t.TempDir()
		res, err := Generate(dir, Config{Issues: 1, CommentsPerIssue: 0})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.IssueRefs) != 1 {
			t.Fatalf("got %d refs, want 1", len(res.IssueRefs))
		}
		repo, err := chain.OpenRepository(dir)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := repo.Reference(plumbing.ReferenceName(res.IssueRefs[0]), true)
		if err != nil {
			t.Fatal(err)
		}
		events, err := chain.WalkChain(repo, ref.Hash())
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("got %d events, want 1", len(events))
		}
		files, err := chain.ReadTree(repo, ref.Hash())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := files["meta.json"]; !ok {
			t.Fatalf("meta.json missing")
		}
	})

	t.Run("three_issues_two_comments_mirrored", func(t *testing.T) {
		dir := t.TempDir()
		res, err := Generate(dir, Config{
			Issues:               3,
			CommentsPerIssue:     2,
			MirrorRemoteTracking: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.IssueRefs) != 3 {
			t.Fatalf("got %d refs, want 3", len(res.IssueRefs))
		}
		repo, err := chain.OpenRepository(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.IssueRefs {
			localRef, err := repo.Reference(plumbing.ReferenceName(r), true)
			if err != nil {
				t.Fatal(err)
			}
			trName, err := refspace.RemoteTrackingRef(r)
			if err != nil {
				t.Fatal(err)
			}
			trRef, err := repo.Reference(plumbing.ReferenceName(trName), true)
			if err != nil {
				t.Fatal(err)
			}
			if localRef.Hash() != trRef.Hash() {
				t.Fatalf("tracking hash mismatch for %s", r)
			}
			events, err := chain.WalkChain(repo, localRef.Hash())
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 {
				t.Fatalf("got %d events for %s, want 3", len(events), r)
			}
		}
	})
}
