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
	Warnings      []Issue // diagnostics that do not by themselves fail verification
}

// OK reports whether the ref had zero issues.
func (r RefReport) OK() bool { return len(r.Issues) == 0 }

// VerifyRef walks every commit reachable from ref's current head and
// checks (docs/11-security.md「検証規則」):
//  1. the commit's SSH signature is valid against the users registry
//     (in-process core/sign.Verifier.VerifyCommitObject by default, or
//     core/sign.VerifyCommitLegacy when gpg.ssh.revocationFile,
//     gpg.minTrustLevel, or gpg.ssh.program is configured, including the
//     revoked-key timing rule);
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
type verifyContext struct {
	dir               string
	repo              *git.Repository
	registryState     *materialize.State
	registryErr       error
	emailConflicts    []materialize.EmailConflict
	verifier          *sign.Verifier
	hasRevocationFile bool
}

func newVerifyContext(ctx context.Context, dir, registryOID string) (*verifyContext, error) {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return nil, err
	}
	vctx := &verifyContext{dir: dir, repo: repo}
	if revFile, err := gitx.New(dir).ConfigGet(ctx, "gpg.ssh.revocationFile"); err == nil && strings.TrimSpace(revFile) != "" {
		vctx.hasRevocationFile = true
	}
	if minTrust, err := gitx.New(dir).ConfigGet(ctx, "gpg.minTrustLevel"); err == nil && strings.TrimSpace(minTrust) != "" {
		vctx.hasRevocationFile = true
	}
	if sshProg, err := gitx.New(dir).ConfigGet(ctx, "gpg.ssh.program"); err == nil && strings.TrimSpace(sshProg) != "" {
		vctx.hasRevocationFile = true
	}
	state, err := loadRegistryState(ctx, dir, repo, registryOID)
	if err != nil {
		var shallow *chain.ShallowHistoryError
		if errors.As(err, &shallow) {
			return nil, err
		}
		vctx.registryErr = err
		return vctx, nil
	}
	vctx.registryState = state
	vctx.emailConflicts = materialize.EmailConflicts(state)
	vctx.verifier = sign.NewVerifier(state)
	return vctx, nil
}

func VerifyRef(ctx context.Context, dir, ref string, trustRootPin string) (*RefReport, error) {
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		return nil, err
	}
	if oid == "" {
		return &RefReport{Ref: ref}, nil // ref doesn't exist locally; nothing to verify
	}
	registryOID := ""
	if ref == refspace.UsersRegistryRef {
		registryOID = oid
	}
	vctx, err := newVerifyContext(ctx, dir, registryOID)
	if err != nil {
		return nil, err
	}
	return verifyRefWithContext(ctx, vctx, ref, oid, trustRootPin)
}

func verifyRefWithContext(ctx context.Context, vctx *verifyContext, ref, oid, trustRootPin string) (*RefReport, error) {
	report := &RefReport{Ref: ref}
	if oid == "" {
		return report, nil
	}

	head := plumbing.NewHash(oid)
	commits, err := chain.WalkCommits(vctx.repo, head)
	if err != nil {
		var shallow *chain.ShallowHistoryError
		if errors.As(err, &shallow) {
			return nil, err
		}
		report.Issues = append(report.Issues, Issue{CommitHash: oid, Code: "walk_failed", Message: err.Error()})
		return report, nil
	}
	report.CommitCount = len(commits)

	var ids event.IDSet
	var envelopes []*event.Envelope
	envByHash := make(map[plumbing.Hash]*event.Envelope, len(commits))
	commitByID := map[string]string{}
	parsed, parseErr := refspace.Parse(ref)
	for _, commit := range commits {
		env, err := chain.ExtractEnvelope(commit)
		if err != nil {
			report.Issues = append(report.Issues, Issue{CommitHash: commit.Hash.String(), Code: "invalid_envelope", Message: err.Error()})
			continue
		}
		envByHash[commit.Hash] = env
		if env == nil {
			continue
		}
		if old := commitByID[env.ID]; old == "" || commit.Hash.String() < old {
			commitByID[env.ID] = commit.Hash.String()
		}
		if known, err := event.ValidateDataSchema(env); err != nil {
			report.Issues = append(report.Issues, Issue{CommitHash: commit.Hash.String(), Code: "invalid_event_data", Message: err.Error()})
		} else if !known {
			report.Warnings = append(report.Warnings, Issue{CommitHash: commit.Hash.String(), Code: "unknown_event_kind", Message: fmt.Sprintf("no data schema for %q", env.Kind)})
		}
		if parseErr == nil {
			if err := refspace.ValidateEventContext(parsed, env); err != nil {
				report.Issues = append(report.Issues, Issue{CommitHash: commit.Hash.String(), Code: "event_ref_mismatch", Message: err.Error()})
			}
		}
		envelopes = append(envelopes, env)
		if _, err := ids.Add(env); err != nil {
			code := "invalid_envelope"
			if errors.Is(err, event.ErrConflictingEventID) {
				code = "conflicting_event_id"
			}
			report.Issues = append(report.Issues, Issue{CommitHash: commit.Hash.String(), Code: code, Message: err.Error()})
		}
	}
	if err := event.ValidateEntityOrder(envelopes); err != nil {
		report.Issues = append(report.Issues, Issue{Code: "event_order_violation", Message: err.Error()})
	}
	// A contradictory registry must not become authoritative for signatures.
	if len(report.Issues) > 0 {
		return report, nil
	}
	if parseErr == nil {
		for _, ignored := range materialize.IgnoredStatusTransitions(string(parsed.Feature), envelopes) {
			report.Warnings = append(report.Warnings, Issue{CommitHash: commitByID[ignored.EventID], Code: "ignored_status_transition", Message: fmt.Sprintf("%s from %q to %q was ignored by the reducer", ignored.Kind, ignored.From, ignored.To)})
		}
	}

	if vctx.registryErr != nil {
		report.Issues = append(report.Issues, Issue{Code: "invalid_registry", Message: fmt.Sprintf("cannot verify against the users registry: %v", vctx.registryErr)})
		return report, nil
	}
	for _, conflict := range vctx.emailConflicts {
		report.Issues = append(report.Issues, Issue{Code: "ambiguous_email", Message: fmt.Sprintf("email %q belongs to multiple users: %s", conflict.Email, strings.Join(conflict.Usernames, ", "))})
	}

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

	isRegistry := ref == refspace.UsersRegistryRef
	var adminBefore map[plumbing.Hash]bool
	if isRegistry {
		var err error
		adminBefore, err = computeRegistryAdminBefore(ctx, commits, envByHash, trustRoot)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			report.Issues = append(report.Issues, Issue{Code: "event_order_violation", Message: err.Error()})
			return report, nil
		}
	}

	for _, commit := range commits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		committerEmail := commit.Committer.Email
		commitTime := formatGitTime(commit.Committer.When)

		var result sign.Result
		if vctx.hasRevocationFile {
			var err error
			result, err = sign.VerifyCommitLegacy(ctx, vctx.dir, commit.Hash.String(), vctx.registryState, committerEmail, commitTime)
			if err != nil {
				return nil, err
			}
		} else {
			result = vctx.verifier.VerifyCommitObject(commit, committerEmail, commitTime)
		}
		if !result.Valid {
			report.Issues = append(report.Issues, Issue{
				CommitHash: commit.Hash.String(),
				Code:       "invalid_or_missing_signature",
				Message:    fmt.Sprintf("signature check failed for committer %s", committerEmail),
			})
		}

		env := envByHash[commit.Hash]
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
			if !adminBefore[commit.Hash] {
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
	registryOID := ""
	for _, e := range entries {
		if e.Ref == refspace.UsersRegistryRef {
			registryOID = e.OID
			break
		}
	}
	vctx, err := newVerifyContext(ctx, dir, registryOID)
	if err != nil {
		return nil, err
	}
	var reports []RefReport
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil {
			reports = append(reports, RefReport{Ref: e.Ref, Warnings: []Issue{{Code: "unrecognized_ref", Message: err.Error()}}})
			continue
		}
		switch parsed.Feature {
		case refspace.FeatureIssue, refspace.FeatureTask, refspace.FeatureChat, refspace.FeatureNotify, refspace.FeatureUsers:
		default:
			continue
		}
		report, err := verifyRefWithContext(ctx, vctx, e.Ref, e.OID, trustRootPin)
		if err != nil {
			return nil, err
		}
		reports = append(reports, *report)
	}
	return reports, nil
}

func loadRegistryState(ctx context.Context, dir string, repo *git.Repository, oid string) (*materialize.State, error) {
	if oid == "" {
		r := gitx.New(dir)
		var err error
		oid, err = r.RevParse(ctx, refspace.UsersRegistryRef)
		if err != nil {
			return nil, err
		}
	}
	if oid == "" {
		return materialize.NewState(), nil
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, err
	}
	parsed, _ := refspace.Parse(refspace.UsersRegistryRef)
	for _, env := range events {
		if err := refspace.ValidateEventContext(parsed, env); err != nil {
			return nil, err
		}
		if _, err := event.ValidateDataSchema(env); err != nil {
			return nil, err
		}
	}
	return materialize.UsersRegistry.Fold(events), nil
}

// computeRegistryAdminBefore checks role:admin as of each commit's parent(s)
// without retaining per-commit event slices or state folds across the ledger,
// keeping peak memory O(N) even on heavily branched histories.
func computeRegistryAdminBefore(ctx context.Context, commits []*object.Commit, envByHash map[plumbing.Hash]*event.Envelope, trustRoot string) (map[plumbing.Hash]bool, error) {
	adminOK := make(map[plumbing.Hash]bool, len(commits))
	if len(commits) == 0 {
		return adminOK, nil
	}
	commitMap := make(map[plumbing.Hash]*object.Commit, len(commits))
	for _, c := range commits {
		commitMap[c.Hash] = c
	}

	collectFrom := func(root plumbing.Hash) ([]*event.Envelope, error) {
		visited := map[plumbing.Hash]bool{}
		seenID := map[string]bool{}
		var out []*event.Envelope
		stack := []plumbing.Hash{root}
		for len(stack) > 0 {
			h := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[h] {
				continue
			}
			visited[h] = true
			c := commitMap[h]
			if c == nil {
				continue
			}
			if env := envByHash[h]; env != nil && !seenID[env.ID] {
				seenID[env.ID] = true
				out = append(out, env)
			}
			stack = append(stack, c.ParentHashes...)
		}
		if err := event.ValidateEntityOrder(out); err != nil {
			return nil, err
		}
		return out, nil
	}

	for _, c := range commits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		env := envByHash[c.Hash]
		if env == nil || (c.Hash.String() == trustRoot && len(c.ParentHashes) == 0) {
			continue
		}
		var parentEvents []*event.Envelope
		if len(c.ParentHashes) == 1 {
			pe, err := collectFrom(c.ParentHashes[0])
			if err != nil {
				return nil, err
			}
			parentEvents = pe
		} else if len(c.ParentHashes) > 1 {
			seenID := map[string]bool{}
			for _, p := range c.ParentHashes {
				pe, err := collectFrom(p)
				if err != nil {
					return nil, err
				}
				for _, e := range pe {
					if !seenID[e.ID] {
						seenID[e.ID] = true
						parentEvents = append(parentEvents, e)
					}
				}
			}
			if err := event.ValidateEntityOrder(parentEvents); err != nil {
				return nil, err
			}
		}
		adminOK[c.Hash] = materialize.IsAdmin(materialize.UsersRegistry.Fold(parentEvents), env.Actor)
	}
	return adminOK, nil
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
