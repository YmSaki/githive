// Package fsckapp implements `githive fsck [--compact]`: an integrity pass
// over every ref under refs/projects/ (docs/10-cli-spec.md「コマンド体系」:
// スキーマ検証、チェックポイント作成).
//
// It does three feature-scoped jobs, dispatched on refspace.Feature exactly
// the way verifyapp.VerifyAll scopes its work:
//
//   - issue/task/chat/notify/users are event-sourced entity chains: every
//     commit's event envelope is decoded and schema-validated, and each
//     invalid envelope is reported as a structured Finding (not aborted on,
//     unlike chain.WalkChain which stops at the first bad envelope).
//   - meta/config is event-sourced too but is not one of the entity chains;
//     it gets its own shape-appropriate check: envelope validation plus a
//     config.json schema_version support check (docs/01-architecture.md
//     「バージョニングと互換性」: 自分の対応版より新しい schema_version は拒否).
//   - wiki/main is NOT event-sourced (a plain git branch); it gets the
//     filename-portability check shared with `githive wiki save`
//     (internal/core/wikifs), never event-schema validation.
//
// With --compact it additionally appends a checkpoint event to any entity
// chain past the docs/03-sync-and-concurrency.md threshold (500 events or 90
// days since the last checkpoint). Checkpoints are read shortcuts that fold
// always ignores (internal/core/materialize/materialize.go), so creating one
// never changes the materialized state; compact.go proves this.
package fsckapp

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Finding is one integrity problem fsck found. Commit and Path are set only
// when the problem is scoped to a specific commit (schema validation) or tree
// path (wiki portability); both are empty otherwise. Code is a stable machine
// token for --json and tests.
type Finding struct {
	Ref     string
	Feature string
	Commit  string
	Path    string
	Code    string
	Message string
}

// CheckpointInfo records a checkpoint fsck created under --compact, for
// reporting. It is never populated when Options.Compact is false.
type CheckpointInfo struct {
	Ref        string
	Feature    string
	Entity     string
	EventID    string
	EventCount int
	Reason     string // "event_count" or "age"
}

// Report is the whole-repository fsck result.
type Report struct {
	Findings    []Finding
	Checkpoints []CheckpointInfo
}

// OK reports whether fsck found zero problems.
func (r *Report) OK() bool { return len(r.Findings) == 0 }

// Options controls a Run. The zero value means "validate only, default
// thresholds, real wall clock".
type Options struct {
	// Compact enables checkpoint creation on chains past the threshold.
	Compact bool
	// MaxEvents overrides the events-since-last-checkpoint threshold
	// (0 -> DefaultMaxEvents). Overridable so tests can exercise the count
	// branch without appending 500 real commits.
	MaxEvents int
	// MaxAge overrides the age-since-last-checkpoint threshold
	// (0 -> DefaultMaxAge).
	MaxAge time.Duration
	// Now overrides the clock used for the age threshold (zero -> time.Now).
	Now time.Time
}

// Threshold defaults (docs/03-sync-and-concurrency.md「チェックポイント」:
// 直前のチェックポイントから 500 イベント、または 90 日経過).
const (
	DefaultMaxEvents = 500
	DefaultMaxAge    = 90 * 24 * time.Hour
)

func (o Options) maxEvents() int {
	if o.MaxEvents > 0 {
		return o.MaxEvents
	}
	return DefaultMaxEvents
}

func (o Options) maxAge() time.Duration {
	if o.MaxAge > 0 {
		return o.MaxAge
	}
	return DefaultMaxAge
}

func (o Options) now() time.Time {
	if !o.Now.IsZero() {
		return o.Now
	}
	return time.Now()
}

// Service runs fsck against a single githive-managed repository directory.
type Service struct {
	Dir string
}

// New returns a Service rooted at dir.
func New(dir string) *Service {
	return &Service{Dir: dir}
}

// Run performs the integrity pass over every ref under refs/projects/,
// dispatching on feature (see the package doc). With opts.Compact it also
// appends checkpoints to entity chains past the threshold.
func (s *Service) Run(ctx context.Context, opts Options) (*Report, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, refspace.Root+"/")
	if err != nil {
		return nil, err
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, err
	}

	report := &Report{}
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil {
			// Not a recognized githive ref; leave it alone, mirroring
			// verifyapp.VerifyAll's skip-on-parse-failure behavior.
			continue
		}
		head := plumbing.NewHash(e.OID)
		switch parsed.Feature {
		case refspace.FeatureIssue, refspace.FeatureTask, refspace.FeatureChat,
			refspace.FeatureNotify, refspace.FeatureUsers:
			report.Findings = append(report.Findings, validateChain(repo, e.Ref, parsed.Feature, head)...)
			if opts.Compact {
				cp, findings, err := s.maybeCheckpoint(ctx, repo, e.Ref, parsed, e.OID, opts)
				if err != nil {
					return nil, err
				}
				report.Findings = append(report.Findings, findings...)
				if cp != nil {
					report.Checkpoints = append(report.Checkpoints, *cp)
				}
			}
		case refspace.FeatureMeta:
			report.Findings = append(report.Findings, validateMetaConfig(repo, e.Ref, head)...)
		case refspace.FeatureWiki:
			f, err := checkWikiFilenames(repo, e.Ref, head)
			if err != nil {
				return nil, err
			}
			report.Findings = append(report.Findings, f...)
		default:
			// Unknown feature: not schema-validated as an event chain.
			continue
		}
	}
	return report, nil
}

// validateChain decodes and schema-validates every commit's event envelope on
// an entity chain, collecting one Finding per invalid envelope instead of
// aborting at the first (chain.WalkCommits returns raw commits and never
// decodes, so a corrupt envelope does not stop the walk). Merge commits carry
// no envelope (chain.ExtractEnvelope returns nil) and are skipped.
func validateChain(repo *git.Repository, ref string, feature refspace.Feature, head plumbing.Hash) []Finding {
	var findings []Finding
	commits, err := chain.WalkCommits(repo, head)
	if err != nil {
		return []Finding{{
			Ref: ref, Feature: string(feature), Code: "walk_failed", Message: err.Error(),
		}}
	}
	for _, c := range commits {
		env, err := chain.ExtractEnvelope(c)
		if err != nil {
			findings = append(findings, Finding{
				Ref: ref, Feature: string(feature), Commit: c.Hash.String(),
				Code: "invalid_envelope", Message: err.Error(),
			})
			continue
		}
		// env == nil: a merge commit, nothing envelope-level to check.
		_ = env
	}
	return findings
}

// validateMetaConfig checks refs/projects/meta/config per its actual shape: it
// is event-sourced (a meta.init envelope) so its envelopes are validated, but
// additionally the tree's config.json must declare a schema_version this build
// supports (docs/01-architecture.md「バージョニングと互換性」).
func validateMetaConfig(repo *git.Repository, ref string, head plumbing.Hash) []Finding {
	findings := validateChain(repo, ref, refspace.FeatureMeta, head)

	files, err := chain.ReadTree(repo, head)
	if err != nil {
		return append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "read_tree_failed", Message: err.Error(),
		})
	}
	raw, ok := files["config.json"]
	if !ok {
		return append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "missing_config",
			Message: "meta/config tree has no config.json",
		})
	}
	v, err := event.DecodeGeneric(raw)
	if err != nil {
		return append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "invalid_config",
			Message: fmt.Sprintf("config.json is not valid JSON: %v", err),
		})
	}
	m, ok := v.(map[string]any)
	if !ok {
		return append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "invalid_config",
			Message: "config.json is not a JSON object",
		})
	}
	sv, ok := asInt(m["schema_version"])
	if !ok {
		return append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "invalid_config",
			Message: "config.json has no integer schema_version",
		})
	}
	if sv > event.SchemaVersion {
		findings = append(findings, Finding{
			Ref: ref, Feature: string(refspace.FeatureMeta), Code: "unsupported_schema_version",
			Message: fmt.Sprintf("config.json schema_version %d is newer than supported %d", sv, event.SchemaVersion),
		})
	}
	return findings
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		// event.DecodeGeneric yields json.Number for numbers; probe it via
		// its Float64 without importing encoding/json here.
		type float64er interface{ Float64() (float64, error) }
		if f, ok := v.(float64er); ok {
			if got, err := f.Float64(); err == nil {
				return int(got), true
			}
		}
		return 0, false
	}
}
