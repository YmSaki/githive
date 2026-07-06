// Package verifyapp implements `githive verify`: walking a chain and
// checking every commit's SSH signature and actor/committer consistency
// against the users registry (docs/11-security.md「SSH 署名」「信頼の根」).
package verifyapp

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/sign"
)

// Issue is one problem found on one commit.
type Issue struct {
	CommitHash string
	Code       string
	Message    string
}

// RefReport is the verification result for one ref.
type RefReport struct {
	Ref           string
	CommitCount   int
	TrustRootHash string // only set for refs/projects/users/registry
	Issues        []Issue
}

// OK reports whether the ref had zero issues.
func (r RefReport) OK() bool { return len(r.Issues) == 0 }

// VerifyRef walks every commit reachable from ref's current head and
// checks (docs/11-security.md「検証規則」):
//  1. the commit's SSH signature is valid against the users registry
//     (core/sign.VerifyCommit, including the revoked-key timing rule);
//  2. for non-merge commits, the event's actor equals the commit's
//     committer email;
//  3. (unsigned/no-registry note) if the users registry itself has no
//     users registered yet, signature checks against it will always fail;
//     callers should treat that as "signing not yet configured" rather
//     than a hard failure until docs/11-security.md's TOFU bootstrap
//     (an admin registers themselves and signs the root commit) has
//     happened - this function still reports it as an issue, since
//     whether that's acceptable is a policy decision for the caller/CLI,
//     not this package.
//
// trustRootPin, if non-empty, is compared against the ref's root commit
// hash (only meaningful for refs/projects/users/registry,
// docs/11-security.md「信頼の根」); a mismatch is reported as an issue.
func VerifyRef(ctx context.Context, dir, ref string, trustRootPin string) (*RefReport, error) {
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		return nil, err
	}
	report := &RefReport{Ref: ref}
	if oid == "" {
		return report, nil // ref doesn't exist locally; nothing to verify
	}

	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return nil, err
	}
	head := plumbing.NewHash(oid)
	commits, err := chain.WalkCommits(repo, head)
	if err != nil {
		return nil, err
	}
	report.CommitCount = len(commits)

	registryState, err := loadRegistryState(ctx, dir)
	if err != nil {
		return nil, err
	}

	isRegistry := ref == refspace.UsersRegistryRef

	for _, commit := range commits {
		if len(commit.ParentHashes) == 0 {
			report.TrustRootHash = commit.Hash.String()
		}

		committerEmail := commit.Committer.Email
		commitTime := formatGitTime(commit.Committer.When)

		result, err := sign.VerifyCommit(ctx, dir, commit.Hash.String(), registryState, committerEmail, commitTime)
		if err != nil {
			return nil, err
		}
		if !result.Valid {
			report.Issues = append(report.Issues, Issue{
				CommitHash: commit.Hash.String(),
				Code:       "invalid_or_missing_signature",
				Message:    fmt.Sprintf("signature check failed for committer %s", committerEmail),
			})
		}

		env, err := chain.ExtractEnvelope(commit)
		if err != nil {
			return nil, err
		}
		if env == nil {
			continue // merge commit: no envelope-level checks apply
		}
		if env.Actor != committerEmail {
			report.Issues = append(report.Issues, Issue{
				CommitHash: commit.Hash.String(),
				Code:       "actor_committer_mismatch",
				Message:    fmt.Sprintf("event actor %q does not match committer email %q", env.Actor, committerEmail),
			})
		}
		if isRegistry && !materialize.IsAdmin(registryState, env.Actor) {
			report.Issues = append(report.Issues, Issue{
				CommitHash: commit.Hash.String(),
				Code:       "registry_change_without_admin",
				Message:    fmt.Sprintf("registry change by %q who does not currently hold role:admin", env.Actor),
			})
		}
	}

	if isRegistry && trustRootPin != "" && report.TrustRootHash != "" && trustRootPin != report.TrustRootHash {
		report.Issues = append(report.Issues, Issue{
			CommitHash: report.TrustRootHash,
			Code:       "trust_root_mismatch",
			Message:    fmt.Sprintf("root commit %s does not match githive.trust.root pin %s", report.TrustRootHash, trustRootPin),
		})
	}

	return report, nil
}

// VerifyAll verifies every ref under refs/projects/ that VerifyRef can
// meaningfully check (issue/task/chat/notify/users; wiki uses ordinary git
// history and meta/config has no signing requirement).
func VerifyAll(ctx context.Context, dir string, trustRootPin string) ([]RefReport, error) {
	r := gitx.New(dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/")
	if err != nil {
		return nil, err
	}
	var reports []RefReport
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil {
			continue
		}
		switch parsed.Feature {
		case refspace.FeatureIssue, refspace.FeatureTask, refspace.FeatureChat, refspace.FeatureNotify, refspace.FeatureUsers:
		default:
			continue
		}
		report, err := VerifyRef(ctx, dir, e.Ref, trustRootPin)
		if err != nil {
			return nil, err
		}
		reports = append(reports, *report)
	}
	return reports, nil
}

func loadRegistryState(ctx context.Context, dir string) (*materialize.State, error) {
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, refspace.UsersRegistryRef)
	if err != nil {
		return nil, err
	}
	if oid == "" {
		return materialize.NewState(), nil
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return nil, err
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, err
	}
	return materialize.UsersRegistry.Fold(events), nil
}

func formatGitTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// TrustRootPin reads githive.trust.root from git config (empty if unset,
// docs/11-security.md「信頼の根」).
func TrustRootPin(ctx context.Context, dir string) (string, error) {
	r := gitx.New(dir)
	return r.ConfigGet(ctx, "githive.trust.root")
}

// SelfEmail is a small convenience re-export so CLI code doesn't need to
// import core/identity directly just for this one call.
func SelfEmail(ctx context.Context, dir string) (string, error) {
	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		return "", err
	}
	return sig.Email, nil
}
