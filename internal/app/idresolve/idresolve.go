// Package idresolve resolves user-supplied entity and related event IDs before
// app writers create events. CLI and MCP share these app-level rules.
package idresolve

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/apperr"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ErrNotFound means no existing entity or eligible event matches the input.
var ErrNotFound = errors.New("idresolve: referenced entity or event not found")

// Match resolves a full ID or a unique prefix of at least eight characters.
// ULID input is case insensitive; stored IDs and returned candidates are full
// lowercase IDs. The existing ULID grammar preserves exact legacy ref IDs.
func Match(input string, candidates []string) (string, error) {
	prefix, err := normalizePrefix(input)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var matches []string
	for _, id := range candidates {
		if strings.HasPrefix(id, prefix) && !seen[id] {
			seen[id] = true
			matches = append(matches, id)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return "", &apperr.AmbiguousIDError{Prefix: prefix, Candidates: matches}
	}
}

// Entity resolves a per-entity feature reference, never an ID belonging to a
// different namespace. Full IDs also require an existing ref.
func Entity(ctx context.Context, dir, feature, input string) (string, error) {
	kind := refspace.Feature(feature)
	if kind != refspace.FeatureIssue && kind != refspace.FeatureTask && kind != refspace.FeatureChat {
		return "", &event.InvalidInputError{Kind: "reference", Field: "kind", Rule: "must be issue, task, or chat"}
	}
	normalized, err := normalizePrefix(input)
	if err != nil {
		return "", err
	}
	r := gitx.New(dir)
	if event.IsValidULID(normalized) {
		ref, err := refspace.EntityRef(kind, normalized)
		if err != nil {
			return "", err
		}
		oid, err := r.RevParse(ctx, ref)
		if err != nil {
			return "", err
		}
		if oid == "" {
			return "", ErrNotFound
		}
		return normalized, nil
	}
	entries, err := r.ForEachRef(ctx, refspace.Root+"/"+feature+"/")
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		parsed, err := refspace.Parse(entry.Ref)
		if err == nil && parsed.Feature == kind && parsed.ID != "" {
			ids = append(ids, parsed.ID)
		}
	}
	return Match(normalized, ids)
}

// Event resolves only events of kind in the named entity's complete history.
// Hidden/superseded events remain addressable; events from other entities and
// event kinds are never valid reply or replacement targets.
func Event(ctx context.Context, dir, feature, entity, input, kind string) (string, error) {
	if _, err := normalizePrefix(input); err != nil {
		return "", err
	}
	ref, err := refspace.EntityRef(refspace.Feature(feature), entity)
	if err != nil {
		return "", err
	}
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		return "", err
	}
	if oid == "" {
		return "", ErrNotFound
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return "", err
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		return "", err
	}
	var ids []string
	for _, env := range events {
		if env.Kind == kind && env.Entity == entity {
			ids = append(ids, env.ID)
		}
	}
	return Match(input, ids)
}

// Related resolves a weak cross-entity link. A full ID is already unambiguous
// and may name an offline/unfetched entity; prefixes require a local match in
// the specified feature. An opaque source kind can only carry a full ID.
func Related(ctx context.Context, dir, feature, input string, opaqueKind bool) (string, error) {
	known := feature == "issue" || feature == "task" || feature == "chat"
	if !known && !opaqueKind {
		return "", &event.InvalidInputError{Kind: "reference", Field: "kind", Rule: "must be issue, task, or chat"}
	}
	normalized := strings.ToLower(input)
	if event.IsValidULID(normalized) {
		return normalized, nil
	}
	if !known {
		return "", &event.InvalidInputError{Kind: "notify.post", Field: "source.id", Rule: "unknown source kind requires a full ULID"}
	}
	return Entity(ctx, dir, feature, input)
}

func normalizePrefix(input string) (string, error) {
	prefix := strings.ToLower(input)
	if len(prefix) < 8 || len(prefix) > 26 || strings.IndexFunc(prefix, func(r rune) bool { return !strings.ContainsRune("0123456789abcdefghjkmnpqrstvwxyz", r) }) >= 0 {
		return "", &event.InvalidInputError{Kind: "reference", Field: "id", Rule: "must be a full ULID or a prefix of at least eight ULID characters"}
	}
	return prefix, nil
}
