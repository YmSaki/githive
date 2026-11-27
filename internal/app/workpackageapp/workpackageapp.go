// Package workpackageapp implements the workpackage feature (CLI alias
// "wp"): WBS decomposition, execution dependencies, claims, submissions and
// acceptance records over refs/projects/workpackage/<id>. Fold stays
// ref-local (materialize.WorkpackageRegistry); cross-ref state is derived by
// internal/core/wpgraph at read time and never stored.
package workpackageapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/apperr"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/app/wptree"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ErrNotFound means no workpackage (or named submission/claim) matched.
var ErrNotFound = errors.New("workpackageapp: workpackage not found")

// ConflictError is a precondition/race rejection (CLI exit code 3):
// stale_acceptance_version, stale_submission, claim_held, claim_lost,
// claim_expired, claim_released, unauthorized_claim_op, already_cancelled.
// Nothing was recorded when it is returned from a write.
type ConflictError struct {
	Code    string
	Message string
	Data    map[string]any
}

func (e *ConflictError) Error() string { return e.Code + ": " + e.Message }

// RegistryInvalidError means a direct operation needed a healthy users
// registry but its history is corrupt (CLI verify_failed, exit code 4).
type RegistryInvalidError struct{ Reason string }

func (e *RegistryInvalidError) Error() string {
	return "verify_failed: users registry history is invalid: " + e.Reason
}

// DefaultLease is wp start's default lease duration.
const DefaultLease = 24 * time.Hour

var commitPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

const feature = "workpackage"

// Service operates on workpackages within one repository directory.
type Service struct {
	Dir    string
	writer *entitychain.Writer
	// now is the clock for time-dependent reads (active claims); tests may
	// replace it.
	now func() time.Time
	// afterPrefetch is a test seam run right after start's snapshot is
	// taken, to simulate a concurrent fetch rewriting tracking refs.
	afterPrefetch func()
	// afterRefList is a test seam run right after observe enumerates the
	// local refs, to simulate a writer moving a ref mid-read.
	afterRefList func()
}

// New returns a Service rooted at dir.
func New(dir string) *Service {
	return &Service{
		Dir: dir,
		writer: &entitychain.Writer{
			Dir: dir, RefFor: RefFor, Registry: materialize.WorkpackageRegistry,
			TreeFiles: wptree.TreeFiles, NotFoundErr: ErrNotFound,
		},
		now: time.Now,
	}
}

// RefFor maps a workpackage ID to its ref name.
func RefFor(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName(refspace.Root + "/workpackage/" + id)
}

func invalid(kind, field, rule string) error {
	return &event.InvalidInputError{Kind: "workpackage." + kind, Field: field, Rule: rule}
}

func (s *Service) envelope(kind, id string, data map[string]any) *event.Envelope {
	eid, ts := idgen.NewWithTimestamp()
	return &event.Envelope{V: event.SchemaVersion, Kind: "workpackage." + kind, ID: eid, TS: ts, Entity: id, Data: data, Extra: map[string]any{}}
}

func (s *Service) append(ctx context.Context, id, kind, summary string, data map[string]any) (*materialize.State, error) {
	return s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		return s.envelope(kind, id, data), "workpackage." + kind + ": " + summary
	})
}

// requireComplete applies the shared writer's shallow rule before any ID
// resolution, so shallow repositories report shallow_history first.
func (s *Service) requireComplete() error {
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return err
	}
	return chain.RequireCompleteProjects(repo)
}

// ResolveID resolves a full ID or a >=8 char prefix of a local workpackage.
func (s *Service) ResolveID(ctx context.Context, prefix string) (string, error) {
	id, err := idresolve.Entity(ctx, s.Dir, feature, prefix)
	if errors.Is(err, idresolve.ErrNotFound) {
		return "", ErrNotFound
	}
	return id, err
}

// resolveSparse accepts a full ULID even if the workpackage is not fetched;
// prefixes must match a local ref.
func (s *Service) resolveSparse(ctx context.Context, input string) (string, error) {
	if lower := toLower(input); event.IsValidULID(lower) {
		return lower, nil
	}
	return s.ResolveID(ctx, input)
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// stateAt folds the chain ending at oid (nil Meta when oid is zero).
func (s *Service) stateAt(oid string) (*materialize.State, error) {
	if oid == gitx.ZeroOID || oid == "" {
		return materialize.NewState(), nil
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, err
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, err
	}
	return materialize.WorkpackageRegistry.Fold(events), nil
}

// fold returns id's current state or ErrNotFound.
func (s *Service) fold(ctx context.Context, id string) (*materialize.State, error) {
	state, err := s.writer.Fold(ctx, id)
	if err != nil {
		return nil, err
	}
	if state.Meta == nil {
		return nil, ErrNotFound
	}
	return state, nil
}

func actor(ctx context.Context, dir string) (string, error) {
	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		return "", err
	}
	return sig.Email, nil
}

// ---- create / finding ----

// CreateInput describes wp new / wp finding.
type CreateInput struct {
	Title     string
	Body      *string
	Kind      string
	Priority  string
	Parent    string   // id or prefix; "" = none
	DependsOn []string // ids or prefixes
	// FindingOf is set by Finding.
	findingOf map[string]any
}

// Create records a new workpackage and returns its ID.
func (s *Service) Create(ctx context.Context, in CreateInput) (string, error) {
	if err := s.requireComplete(); err != nil {
		return "", err
	}
	id := idgen.New()
	data := map[string]any{"title": in.Title}
	if in.Body != nil {
		data["body"] = *in.Body
	}
	if in.Kind != "" {
		if in.Kind != "work" && in.Kind != "container" {
			return "", invalid("create", "kind", "must be work or container")
		}
		data["kind"] = in.Kind
	}
	if in.Priority != "" {
		if in.Priority != "low" && in.Priority != "normal" && in.Priority != "high" {
			return "", invalid("create", "priority", "must be low, normal or high")
		}
		data["priority"] = in.Priority
	}
	if in.Parent != "" {
		parent, err := s.ResolveID(ctx, in.Parent)
		if err != nil {
			return "", err
		}
		data["parent_id"] = parent
	}
	if len(in.DependsOn) > 0 {
		deps := make([]any, 0, len(in.DependsOn))
		for _, raw := range in.DependsOn {
			dep, err := s.ResolveID(ctx, raw)
			if err != nil {
				return "", err
			}
			deps = append(deps, dep)
		}
		// A brand-new workpackage has no dependents, so no cycle can form.
		data["depends_on"] = deps
	}
	if in.findingOf != nil {
		data["finding_of"] = in.findingOf
	}
	summary := in.Title
	if _, err := s.writer.Append(ctx, id, func() (*event.Envelope, string) {
		return s.envelope("create", id, data), "workpackage.create: " + summary
	}); err != nil {
		return "", err
	}
	return id, nil
}

// Finding records a bug branch against parentID's submission (submitID
// prefix, or the current submission when empty).
func (s *Service) Finding(ctx context.Context, parentID, submitID string, in CreateInput) (string, error) {
	if err := s.requireComplete(); err != nil {
		return "", err
	}
	parent, err := s.ResolveID(ctx, parentID)
	if err != nil {
		return "", err
	}
	state, err := s.fold(ctx, parent)
	if err != nil {
		return "", err
	}
	sub, err := selectSubmission(state.Meta, submitID, "finding")
	if err != nil {
		return "", err
	}
	in.Kind = "work"
	in.findingOf = map[string]any{"workpackage": parent, "submit_id": sub}
	return s.Create(ctx, in)
}

// selectSubmission resolves --submit-id (explicit prefix over all
// submissions) or defaults to current_submission_id.
func selectSubmission(meta map[string]any, input, op string) (string, error) {
	subs, _ := meta["submissions"].([]any)
	if input == "" {
		cur, _ := meta["current_submission_id"].(string)
		if cur == "" {
			return "", invalid(op, "submit_id", "the workpackage has no submission")
		}
		return cur, nil
	}
	var ids []string
	for _, raw := range subs {
		if m, ok := raw.(map[string]any); ok {
			if id, ok := m["event_id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	id, err := idresolve.Match(input, ids)
	if errors.Is(err, idresolve.ErrNotFound) {
		return "", fmt.Errorf("%w: submission %q", ErrNotFound, input)
	}
	return id, err
}

// ---- edit / dependency / link / note / cancel / waive ----

// EditInput carries optional edits; nil fields are left unchanged.
type EditInput struct {
	Title, Body, Priority *string
	Parent                *string
	ClearParent           bool
}

// Edit appends a workpackage.edit.
func (s *Service) Edit(ctx context.Context, id string, in EditInput) error {
	if err := s.requireComplete(); err != nil {
		return err
	}
	data := map[string]any{}
	if in.Title != nil {
		data["title"] = *in.Title
	}
	if in.Body != nil {
		data["body"] = *in.Body
	}
	if in.Priority != nil {
		data["priority"] = *in.Priority
	}
	if in.Parent != nil && in.ClearParent {
		return invalid("edit", "parent_id", "--parent and --clear-parent are mutually exclusive")
	}
	if in.ClearParent {
		data["parent_id"] = nil
	}
	if in.Parent != nil {
		parent, err := s.ResolveID(ctx, *in.Parent)
		if err != nil {
			return err
		}
		if parent == id {
			return invalid("edit", "parent_id", "must not be the workpackage itself")
		}
		data["parent_id"] = parent
	}
	_, err := s.append(ctx, id, "edit", "edit", data)
	return err
}

// Dep adds or removes a depends_on edge. Removal of a full 26-char ULID
// needs no lookup, so an unfetched target can be removed to repair a cycle.
func (s *Service) Dep(ctx context.Context, id, target string, remove bool) error {
	if err := s.requireComplete(); err != nil {
		return err
	}
	var resolved string
	var err error
	if remove {
		resolved, err = s.resolveSparse(ctx, target)
	} else {
		resolved, err = s.ResolveID(ctx, target)
	}
	if err != nil {
		return err
	}
	if resolved == id {
		return invalid("dependency", "target", "must not be the workpackage itself")
	}
	data := map[string]any{"target": resolved}
	if remove {
		data["remove"] = true
	} else if err := s.checkNoCycle(ctx, id, resolved); err != nil {
		return err
	}
	_, err = s.append(ctx, id, "dependency", resolved, data)
	return err
}

// checkNoCycle rejects id -> target when target already reaches id through
// depends_on in the locally known (complete) graph.
func (s *Service) checkNoCycle(ctx context.Context, id, target string) error {
	ids, err := s.localIDs(ctx)
	if err != nil {
		return err
	}
	deps := map[string][]string{}
	for _, wp := range ids {
		state, err := s.writer.Fold(ctx, wp)
		if err != nil {
			return err
		}
		if state.Meta == nil {
			continue
		}
		raw, _ := state.Meta["depends_on"].([]any)
		for _, d := range raw {
			if ds, ok := d.(string); ok {
				deps[wp] = append(deps[wp], ds)
			}
		}
	}
	seen := map[string]bool{}
	stack := []string{target}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if u == id {
			return invalid("dependency", "target", "would create a depends_on cycle")
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		stack = append(stack, deps[u]...)
	}
	return nil
}

var linkRels = map[string]bool{"issue": true, "task": true, "chat": true, "workpackage": true}

// Link adds or removes a {rel,id} link.
func (s *Service) Link(ctx context.Context, id, rel, linked string, remove bool) error {
	if !linkRels[rel] {
		return invalid("link", "rel", "must be issue, task, chat or workpackage")
	}
	resolved, err := idresolve.Related(ctx, s.Dir, rel, linked, false)
	if errors.Is(err, idresolve.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if rel == "workpackage" && resolved == id {
		return invalid("link", "id", "must not reference the workpackage itself")
	}
	data := map[string]any{"rel": rel, "id": resolved}
	if remove {
		data["remove"] = true
	}
	_, err = s.append(ctx, id, "link", rel+":"+resolved, data)
	return err
}

// Note appends a workpackage.note.
func (s *Service) Note(ctx context.Context, id, body string) error {
	if body == "" {
		return invalid("note", "body", "must be a non-empty string")
	}
	_, err := s.append(ctx, id, "note", summarize(body), map[string]any{"body": body})
	return err
}

// Cancel appends a workpackage.cancel.
func (s *Service) Cancel(ctx context.Context, id, reason string) error {
	data := map[string]any{}
	if reason != "" {
		data["reason"] = reason
	}
	_, err := s.append(ctx, id, "cancel", summarize(reason), data)
	return err
}

// Waive records a waiver of finding target on workpackage id.
func (s *Service) Waive(ctx context.Context, id, target, reason string) error {
	if reason == "" {
		return invalid("waive", "reason", "must be a non-empty string")
	}
	if err := s.requireComplete(); err != nil {
		return err
	}
	resolved, err := s.resolveSparse(ctx, target)
	if err != nil {
		return err
	}
	if resolved == id {
		return invalid("waive", "target_id", "must not be the workpackage itself")
	}
	_, err = s.append(ctx, id, "waive", resolved, map[string]any{"target_id": resolved, "reason": reason})
	return err
}

func summarize(body string) string {
	runes := []rune(body)
	if len(runes) > 40 {
		return string(runes[:40])
	}
	return body
}

// ---- claims ----

// ValidateLease is the single input boundary for claim/renew durations.
// lease_until is written at millisecond precision on top of a millisecond
// ts, so any lease under 1ms (0s, 1ns, 999µs) would truncate to
// lease_until <= ts and be expired the moment it is appended.
func ValidateLease(op string, lease time.Duration) error {
	if lease < time.Millisecond {
		return invalid(op, "lease_until", "lease must be at least 1ms")
	}
	if lease > materialize.WorkpackageLeaseMax {
		return invalid(op, "lease_until", "lease must not exceed 7 days")
	}
	return nil
}

func leaseUntil(op, ts string, lease time.Duration) (string, error) {
	if err := ValidateLease(op, lease); err != nil {
		return "", err
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return "", err
	}
	return at.Add(lease).UTC().Format(materialize.WorkpackageTimeLayout), nil
}

func claimList(meta map[string]any) []map[string]any {
	raw, _ := meta["claims"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// selectClaim implements the --claim-id resolution table for renew/release.
func selectClaim(meta map[string]any, input, self, ts, op string) (map[string]any, error) {
	claims := claimList(meta)
	if input != "" {
		var ids []string
		for _, c := range claims {
			ids = append(ids, c["claim_id"].(string))
		}
		id, err := idresolve.Match(input, ids)
		if errors.Is(err, idresolve.ErrNotFound) {
			return nil, fmt.Errorf("%w: claim %q", ErrNotFound, input)
		}
		if err != nil {
			return nil, err
		}
		for _, c := range claims {
			if c["claim_id"] == id {
				return c, checkClaim(c, self, ts, op)
			}
		}
	}
	var candidates []map[string]any
	for _, c := range claims {
		if c["holder"] == self && checkClaim(c, self, ts, op) == nil {
			candidates = append(candidates, c)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, invalid(op, "claim_id", "no eligible claim held by you")
	case 1:
		return candidates[0], nil
	default:
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c["claim_id"].(string)
		}
		return nil, &apperr.AmbiguousIDError{Prefix: "", Candidates: ids}
	}
}

func checkClaim(c map[string]any, self, ts, op string) error {
	id, _ := c["claim_id"].(string)
	if c["holder"] != self {
		return &ConflictError{Code: "unauthorized_claim_op", Message: fmt.Sprintf("claim %s is held by %v", id, c["holder"]), Data: map[string]any{"claim_id": id}}
	}
	if c["released_at"] != nil {
		return &ConflictError{Code: "claim_released", Message: fmt.Sprintf("claim %s is already released", id), Data: map[string]any{"claim_id": id}}
	}
	if until, _ := c["lease_until"].(string); op == "renew" && ts > until {
		return &ConflictError{Code: "claim_expired", Message: fmt.Sprintf("claim %s expired at %s", id, until), Data: map[string]any{"claim_id": id}}
	}
	return nil
}

// Renew extends one of the actor's live claims.
func (s *Service) Renew(ctx context.Context, id, claimID string, lease time.Duration, branch string) (string, error) {
	return s.claimOp(ctx, id, claimID, "renew", func(c map[string]any, ts string) (map[string]any, error) {
		until, err := leaseUntil("renew", ts, lease)
		if err != nil {
			return nil, err
		}
		data := map[string]any{"claim_id": c["claim_id"], "lease_until": until}
		if branch != "" {
			data["branch"] = branch
		}
		return data, nil
	})
}

// Release releases one of the actor's unreleased claims (expired included).
func (s *Service) Release(ctx context.Context, id, claimID, note string) (string, error) {
	return s.claimOp(ctx, id, claimID, "release", func(c map[string]any, _ string) (map[string]any, error) {
		data := map[string]any{"claim_id": c["claim_id"]}
		if note != "" {
			data["note"] = note
		}
		return data, nil
	})
}

func (s *Service) claimOp(ctx context.Context, id, claimID, op string, build func(map[string]any, string) (map[string]any, error)) (string, error) {
	self, err := actor(ctx, s.Dir)
	if err != nil {
		return "", err
	}
	chosen := ""
	_, err = s.writer.AppendOnto(ctx, id, func(parent string) (*event.Envelope, string, error) {
		state, err := s.stateAt(parent)
		if err != nil {
			return nil, "", err
		}
		if state.Meta == nil {
			return nil, "", ErrNotFound
		}
		env := s.envelope(op, id, nil)
		input := claimID
		if chosen != "" {
			input = chosen // keep the first attempt's selection on CAS retries
		}
		c, err := selectClaim(state.Meta, input, self, env.TS, op)
		if err != nil {
			return nil, "", err
		}
		chosen = c["claim_id"].(string)
		data, err := build(c, env.TS)
		if err != nil {
			return nil, "", err
		}
		env.Data = data
		return env, "workpackage." + op + ": " + chosen, nil
	})
	return chosen, err
}

// ---- submit / accept ----

// SubmitInput describes wp submit. Commit nil means "use HEAD"; NoCommit
// records commit: null. Reviewers nil means omitted.
type SubmitInput struct {
	Commit    *string
	NoCommit  bool
	Branch    string
	PRURL     string
	Reviewers []string
	Note      string
}

// Submit records a submission against the current acceptance_version. A
// version change before the append lands is rejected, never re-stamped.
func (s *Service) Submit(ctx context.Context, id string, in SubmitInput) (string, error) {
	state, err := s.fold(ctx, id)
	if err != nil {
		return "", err
	}
	if state.Meta["kind"] != "work" {
		return "", invalid("submit", "kind", "only work packages accept submissions")
	}
	version, _ := state.Meta["acceptance_version"].(string)
	data := map[string]any{"acceptance_version": version}
	switch {
	case in.NoCommit && in.Commit != nil:
		return "", invalid("submit", "commit", "--commit and --no-commit are mutually exclusive")
	case in.NoCommit:
		data["commit"] = nil
	default:
		commit := ""
		if in.Commit != nil {
			commit = *in.Commit
		} else {
			commit, err = gitx.New(s.Dir).RevParse(ctx, "HEAD")
			if err != nil {
				return "", err
			}
		}
		if !commitPattern.MatchString(commit) {
			return "", invalid("submit", "commit", "must be a full lowercase 40/64-hex commit OID")
		}
		data["commit"] = commit
	}
	if in.Branch != "" {
		data["branch"] = in.Branch
	}
	if in.PRURL != "" {
		data["pr_url"] = in.PRURL
	}
	if in.Note != "" {
		data["note"] = in.Note
	}
	if in.Reviewers != nil {
		reviewers, err := s.resolveReviewers(ctx, in.Reviewers)
		if err != nil {
			return "", err
		}
		data["reviewers"] = reviewers
	}
	var eid string
	_, err = s.writer.AppendOnto(ctx, id, func(parent string) (*event.Envelope, string, error) {
		current, err := s.stateAt(parent)
		if err != nil {
			return nil, "", err
		}
		if current.Meta == nil {
			return nil, "", ErrNotFound
		}
		if now, _ := current.Meta["acceptance_version"].(string); now != version {
			return nil, "", &ConflictError{Code: "stale_acceptance_version", Message: fmt.Sprintf("acceptance_version moved from %s to %s; review the new body and submit again", version, now),
				Data: map[string]any{"acceptance_version": now, "expected": version}}
		}
		env := s.envelope("submit", id, data)
		eid = env.ID
		return env, "workpackage.submit", nil
	})
	return eid, err
}

// Accept records acceptance of submitID (prefix) or the current submission.
func (s *Service) Accept(ctx context.Context, id, submitID, note string) (string, error) {
	state, err := s.fold(ctx, id)
	if err != nil {
		return "", err
	}
	if state.Meta["kind"] != "work" {
		return "", invalid("accept", "kind", "only work packages accept approvals")
	}
	target, err := selectSubmission(state.Meta, submitID, "accept")
	if err != nil {
		return "", err
	}
	data := map[string]any{"submit_id": target}
	if note != "" {
		data["note"] = note
	}
	var eid string
	_, err = s.writer.AppendOnto(ctx, id, func(parent string) (*event.Envelope, string, error) {
		current, err := s.stateAt(parent)
		if err != nil {
			return nil, "", err
		}
		if current.Meta == nil {
			return nil, "", ErrNotFound
		}
		cur, _ := current.Meta["current_submission_id"].(string)
		if cur != target {
			return nil, "", &ConflictError{Code: "stale_submission", Message: fmt.Sprintf("submission %s is not the current submission %s", target, cur),
				Data: map[string]any{"submit_id": target, "current_submission_id": cur}}
		}
		for _, raw := range current.Meta["submissions"].([]any) {
			sub := raw.(map[string]any)
			if sub["event_id"] == cur && sub["acceptance_version"] != current.Meta["acceptance_version"] {
				return nil, "", &ConflictError{Code: "stale_acceptance_version", Message: "the current submission was made against an older acceptance_version; a new submission is required",
					Data: map[string]any{"submit_id": target, "acceptance_version": current.Meta["acceptance_version"]}}
			}
		}
		env := s.envelope("accept", id, data)
		eid = env.ID
		return env, "workpackage.accept: " + target, nil
	})
	return eid, err
}
