// Package refspace builds, parses, and validates githive ref names.
// All githive data lives under refs/projects/ (docs/02-data-model.md,
// ADR-0006). This package is pure: it does no I/O and does not talk to git
// itself (see internal/core/gitx for that).
package refspace

import (
	"fmt"
	"regexp"
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

// shardedFeatures are the features that, alongside their singleton ref,
// also accept monthly shard refs (refs/projects/<feature>/<yyyy-mm>) for a
// future rotation away from a single ever-growing ref
// (docs/features/notify.md「シャーディング（将来）」, ADR-0006「単一 ref の機能
// も <kind>/<name> 形式にしておけば、後からのシャード追加・並置が ref 名の変更
// なしにできる」). Only notify has this today; the reader-side aggregation
// across stream + shards and the write-path rotation itself are deferred to
// the actual migration (D8: this package only parses and validates the
// shard ref shape ahead of time).
var shardedFeatures = map[Feature]bool{
	FeatureNotify: true,
}

// shardRe matches a monthly shard name: a 4-digit year and a 2-digit month
// 01-12, e.g. "2026-07". Anything else (a 1-digit month, an out-of-range
// month, extra characters) is rejected, same as any other malformed ref.
var shardRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

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
	ID      string // ULID entity id; empty for singleton and shard refs.
	// Shard is the yyyy-mm name for a notify monthly shard ref
	// (refs/projects/notify/<yyyy-mm>, docs/features/notify.md「シャーディ
	// ング（将来）」); empty for every other ref. It is a separate field from
	// ID (never both set) so per-entity consumers that key off ID
	// (FilterByFeature, issueapp/taskapp/chatapp's readers) cannot mistake
	// a shard name for an entity ULID.
	Shard string
}

// Parse decomposes a ref name under Root into its feature (and entity ID,
// for per-entity features, or shard name, for a sharded feature's monthly
// ref). It rejects anything outside the known namespace/feature set.
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
	name := parts[1]

	if shardedFeatures[feature] && shardRe.MatchString(name) {
		return ParsedRef{Feature: feature, Shard: name}, nil
	}

	if !perEntityFeatures[feature] {
		return ParsedRef{}, fmt.Errorf("refspace: unknown or non-per-entity feature %q in ref %q", parts[0], ref)
	}
	if !event.IsValidULID(name) {
		return ParsedRef{}, fmt.Errorf("refspace: invalid entity id %q in ref %q", name, ref)
	}
	return ParsedRef{Feature: feature, ID: name}, nil
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

// LocalRefFromTracking is RemoteTrackingRef's inverse: it maps a
// refs/githive-remote/... ref back to its refs/projects/... counterpart.
// Sync uses this to discover entities that exist only on the remote (e.g.
// an issue another clone created) so they can be fast-forwarded in, not
// just refresh refs this clone already has locally
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
func LocalRefFromTracking(trackingRef string) (string, error) {
	rest, ok := strings.CutPrefix(trackingRef, RemoteTrackingRoot+"/")
	if !ok {
		return "", fmt.Errorf("refspace: ref %q is not under %s/", trackingRef, RemoteTrackingRoot)
	}
	return Root + "/" + rest, nil
}
