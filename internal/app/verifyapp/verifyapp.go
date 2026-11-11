// Package verifyapp implements `githive verify`: walking a chain and
// checking every commit's SSH signature and actor/committer consistency
// against the users registry (docs/11-security.md「SSH 署名」「信頼の根」).
package verifyapp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
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
// For refs/projects/users/registry it additionally checks
// (docs/11-security.md「信頼の根」, 検証規則 4):
//   - every non-root registry change is by a user holding role:admin in the
//     registry as of its parent commit(s), not as of HEAD (#109);
//   - the history has a single root: every root other than trustRootPin
//     (all of them, if no pin is set) is reported as multiple_trust_roots,
//     and a pin matching no root as trust_root_mismatch (#113).
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

	var ids event.IDSet
	for _, commit := range commits {
		env, err := chain.ExtractEnvelope(commit)
		if err != nil || env == nil {
			continue
		}
		if _, err := ids.Add(env); err != nil {
			code := "invalid_envelope"
			if errors.Is(err, event.ErrConflictingEventID) {
				code = "conflicting_event_id"
			}
			report.Issues = append(report.Issues, Issue{CommitHash: commit.Hash.String(), Code: code, Message: err.Error()})
		}
	}
	// A contradictory registry must not become authoritative for signatures.
	if len(report.Issues) > 0 {
		return report, nil
	}

	registryState, err := loadRegistryState(ctx, dir)
	if err != nil {
		return nil, err
	}

	isRegistry := ref == refspace.UsersRegistryRef

	var roots []string
	for _, commit := range commits {
		if len(commit.ParentHashes) == 0 {
			roots = append(roots, commit.Hash.String())
		}
	}
	sort.Strings(roots)
	trustRoot := ""
	switch {
	case trustRootPin != "":
		trustRoot = trustRootPin
	case len(roots) == 1:
		trustRoot = roots[0]
	}
	if len(roots) > 0 {
		report.TrustRootHash = roots[0]
		if slices.Contains(roots, trustRoot) {
			report.TrustRootHash = trustRoot
		}
	}

	for _, commit := range commits {
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
		if isRegistry {
			// 規則 4 は変更時点（= 親までのイベントを fold した台帳）で判定する。
			// 信頼の根のルートコミットだけは、台帳が空の時点で admin を
			// 自己登録する唯一の正当な書き込みなので対象外とする。
			if commit.Hash.String() == trustRoot && len(commit.ParentHashes) == 0 {
				continue
			}
			before, err := registryStateBefore(repo, commit)
			if err != nil {
				return nil, err
			}
			if !materialize.IsAdmin(before, env.Actor) {
				report.Issues = append(report.Issues, Issue{
					CommitHash: commit.Hash.String(),
					Code:       "registry_change_without_admin",
					Message:    fmt.Sprintf("registry change by %q who did not hold role:admin in the registry as of its parent commit(s)", env.Actor),
				})
			}
		}
	}

	if isRegistry {
		if trustRootPin != "" && len(roots) > 0 && !slices.Contains(roots, trustRootPin) {
			report.Issues = append(report.Issues, Issue{
				CommitHash: roots[0],
				Code:       "trust_root_mismatch",
				Message:    fmt.Sprintf("root commit(s) %s do not match githive.trust.root pin %s", strings.Join(roots, ", "), trustRootPin),
			})
		}
		// docs/11「信頼の根」: registry が無関係な複数のルートを持つことは
		// 正常系に存在しない（sync は無関係な台帳を合流させない）。pin 以外の
		// ルートはすべて、pin が無ければ全ルートを報告する。
		if len(roots) > 1 {
			for _, root := range roots {
				if root == trustRootPin {
					continue
				}
				report.Issues = append(report.Issues, Issue{
					CommitHash: root,
					Code:       "multiple_trust_roots",
					Message:    fmt.Sprintf("registry history has %d root commits (%s); %s is not the pinned trust root", len(roots), strings.Join(roots, ", "), root),
				})
			}
		}
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

// registryStateBefore folds every registry event reachable from commit's
// parents: the registry "as of" the moment commit was written
// (docs/11-security.md 検証規則 4「変更時点の台帳」). A root commit gets the
// empty state. This re-walks the ancestry per commit (quadratic in the
// registry's length), which is acceptable for a ledger of users/keys.
func registryStateBefore(repo *git.Repository, commit *object.Commit) (*materialize.State, error) {
	seen := map[string]bool{}
	var events []*event.Envelope
	for _, parent := range commit.ParentHashes {
		envs, err := chain.WalkChain(repo, parent)
		if err != nil {
			return nil, err
		}
		for _, env := range envs {
			if !seen[env.ID] {
				seen[env.ID] = true
				events = append(events, env)
			}
		}
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
