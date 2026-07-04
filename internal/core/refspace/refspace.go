// Package refspace builds, parses, and validates githive ref names.
// All githive data lives under refs/projects/ (docs/02-data-model.md,
// ADR-0006). This package is pure: it does no I/O and does not talk to git
// itself (see internal/core/gitx for that).
package refspace

import (
	"fmt"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
)

// Root is the namespace prefix all githive-managed refs live under.
const Root = "refs/projects"

// RemoteTrackingRoot is the separate namespace `githive init` configures
// `git fetch` to land in, kept apart from Root so IDE auto-fetch cannot
// clobber unpushed local chains (docs/02-data-model.md「ローカル作業名前空間と
// リモート追跡の分離」, ADR-0008).
const RemoteTrackingRoot = "refs/githive-remote"

// Feature identifies one of the entity kinds githive manages.
type Feature string

const (
	FeatureIssue  Feature = "issue"
	FeatureTask   Feature = "task"
	FeatureNotify Feature = "notify"
	FeatureUsers  Feature = "users"
	FeatureChat   Feature = "chat"
	FeatureWiki   Feature = "wiki"
	FeatureMeta   Feature = "meta"
)

// perEntityFeatures are the features that have one ref per entity ID
// (refs/projects/<feature>/<id>). The remaining features are singletons.
var perEntityFeatures = map[Feature]bool{
	FeatureIssue: true,
	FeatureTask:  true,
	FeatureChat:  true,
}

// Singleton ref names (docs/02-data-model.md「ref 名前空間」).
const (
	MetaConfigRef    = Root + "/meta/config"
	NotifyStreamRef  = Root + "/notify/stream"
	UsersRegistryRef = Root + "/users/registry"
	WikiMainRef      = Root + "/wiki/main"
)

// EntityRef builds the ref name for a single entity of the given feature.
// Only issue/task/chat are per-entity; other features are singletons and
// must be referenced via their dedicated constant.
func EntityRef(feature Feature, id string) (string, error) {
	if !perEntityFeatures[feature] {
		return "", fmt.Errorf("refspace: feature %q is a singleton, not per-entity", feature)
	}
	if !event.IsValidULID(id) {
		return "", fmt.Errorf("refspace: invalid entity id %q: must be a 26-char lowercase ULID", id)
	}
	return fmt.Sprintf("%s/%s/%s", Root, feature, id), nil
}

// ParsedRef is the result of decomposing a refs/projects/... ref name.
type ParsedRef struct {
	Feature Feature
	ID      string // empty for singleton refs
}

// Parse decomposes a ref name under Root into its feature (and entity ID,
// for per-entity features). It rejects anything outside the known
// namespace/feature set.
func Parse(ref string) (ParsedRef, error) {
	rest, ok := strings.CutPrefix(ref, Root+"/")
	if !ok {
		return ParsedRef{}, fmt.Errorf("refspace: ref %q is not under %s/", ref, Root)
	}
	parts := strings.Split(rest, "/")

	switch ref {
	case MetaConfigRef:
		return ParsedRef{Feature: FeatureMeta}, nil
	case NotifyStreamRef:
		return ParsedRef{Feature: FeatureNotify}, nil
	case UsersRegistryRef:
		return ParsedRef{Feature: FeatureUsers}, nil
	case WikiMainRef:
		return ParsedRef{Feature: FeatureWiki}, nil
	}

	if len(parts) != 2 {
		return ParsedRef{}, fmt.Errorf("refspace: malformed ref %q", ref)
	}
	feature := Feature(parts[0])
	id := parts[1]
	if !perEntityFeatures[feature] {
		return ParsedRef{}, fmt.Errorf("refspace: unknown or non-per-entity feature %q in ref %q", parts[0], ref)
	}
	if !event.IsValidULID(id) {
		return ParsedRef{}, fmt.Errorf("refspace: invalid entity id %q in ref %q", id, ref)
	}
	return ParsedRef{Feature: feature, ID: id}, nil
}

// FilterByFeature returns the refs among candidates that belong to the given
// per-entity feature, sorted by entity ID ascending (which is also creation
// order, since IDs are ULIDs).
func FilterByFeature(candidates []string, feature Feature) []string {
	var out []string
	for _, ref := range candidates {
		parsed, err := Parse(ref)
		if err != nil {
			continue
		}
		if parsed.Feature == feature && parsed.ID != "" {
			out = append(out, ref)
		}
	}
	return out
}

// RemoteTrackingRef maps a refs/projects/... ref to its remote-tracking
// counterpart under RemoteTrackingRoot, per the `git config --add
// remote.origin.fetch '+refs/projects/*:refs/githive-remote/*'` refspec that
// `githive init` installs.
func RemoteTrackingRef(ref string) (string, error) {
	rest, ok := strings.CutPrefix(ref, Root+"/")
	if !ok {
		return "", fmt.Errorf("refspace: ref %q is not under %s/", ref, Root)
	}
	return RemoteTrackingRoot + "/" + rest, nil
}
