// Package logapp implements `githive log`: a cross-feature chronological
// event timeline (docs/10-cli-spec.md「コマンド体系」: `githive log
// [--since] [--actor]`). Unlike the per-feature List calls (issueapp,
// taskapp, ...), this walks raw event envelopes rather than materialized
// state, so it does not touch internal/core/materialize and carries no
// fold-semantics or spec/vectors obligations.
package logapp

import (
	"context"
	"sort"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Service provides read access to the cross-feature event timeline for the
// repository at Dir.
type Service struct {
	Dir string
}

// New returns a Service rooted at dir.
func New(dir string) *Service {
	return &Service{Dir: dir}
}

// Entry is one event from the timeline, tagged with the feature its ref
// belongs to. It is a map (rather than a struct), like the other app
// packages' list items (e.g. notifyapp.Post), because internal/core/event's
// canonical JSON encoder only knows how to render map[string]any/[]any/
// primitives, not arbitrary structs (internal/core/event/canonical.go
// encodeValue). Keys: feature, v, kind, id, ts, actor, entity, data.
type Entry = map[string]any

// timelineFeatures are the features whose events are activity worth showing
// in a timeline. meta (system config) and wiki (not implemented yet, its
// ref may not even exist; docs/13-roadmap.md P4) are excluded.
var timelineFeatures = map[refspace.Feature]bool{
	refspace.FeatureIssue:  true,
	refspace.FeatureTask:   true,
	refspace.FeatureChat:   true,
	refspace.FeatureNotify: true,
	refspace.FeatureUsers:  true,
}

// ListFilter narrows List results. Zero value means "no filter".
type ListFilter struct {
	// Since, when non-empty, restricts to events with TS >= Since. Since
	// must be in the same RFC3339 UTC millisecond format as event
	// envelopes' ts field, so lexical comparison is a valid time
	// comparison.
	Since string
	// ActorEmail, when non-empty, restricts to events whose actor is
	// exactly this email (actor is always a raw email per ADR-0009; no
	// username resolution is performed here).
	ActorEmail string
}

// List returns every event across issue/task/chat/notify/users, merged into
// a single chronological (by ULID) timeline, optionally filtered.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Entry, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, refspace.Root+"/")
	if err != nil {
		return nil, err
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []Entry
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || !timelineFeatures[parsed.Feature] {
			continue
		}
		envelopes, err := chain.WalkChain(repo, plumbing.NewHash(e.OID))
		if err != nil {
			return nil, err
		}
		for _, env := range envelopes {
			if seen[env.ID] {
				continue
			}
			seen[env.ID] = true

			if filter.Since != "" && env.TS < filter.Since {
				continue
			}
			if filter.ActorEmail != "" && env.Actor != filter.ActorEmail {
				continue
			}

			out = append(out, Entry{
				"feature": string(parsed.Feature),
				"v":       env.V,
				"kind":    env.Kind,
				"id":      env.ID,
				"ts":      env.TS,
				"actor":   env.Actor,
				"entity":  env.Entity,
				"data":    env.Data,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i]["id"].(string) < out[j]["id"].(string)
	})
	return out, nil
}
