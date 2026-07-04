// Package initapp implements `githive init`: it configures the remote
// tracking refspec and core.logAllRefUpdates that keep local writes safe
// from IDE auto-fetch (docs/02-data-model.md「ローカル作業名前空間とリモート
// 追跡の分離」, ADR-0008), and creates refs/projects/meta/config if it does
// not exist yet (docs/02-data-model.md「meta/config ref」).
package initapp

import (
	"context"
	"fmt"
	"time"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// SchemaVersion is written into refs/projects/meta/config's config.json
// (docs/02-data-model.md「meta/config ref」).
const SchemaVersion = 1

// DefaultFeatures matches the feature set docs/00-vision.md commits to for
// v0.1 (docs/02-data-model.md「meta/config ref」example).
var DefaultFeatures = []string{"issue", "task", "notify", "users", "chat", "wiki"}

// Result reports what Init actually did, so the CLI can render an
// idempotent "already initialized" message instead of silently no-op'ing.
type Result struct {
	RefspecAdded     bool
	LogAllRefUpdates bool
	MetaConfigMade   bool
}

// Init is idempotent: running it again on an already-initialized repo is a
// no-op (docs/10-cli-spec.md: `githive init` # fetch refspec 追加、meta/config
// 作成（無ければ）、初回 fetch).
func Init(ctx context.Context, dir, remote, project string) (Result, error) {
	var result Result
	r := gitx.New(dir)

	refspecKey := fmt.Sprintf("remote.%s.fetch", remote)
	trackingRefspec := fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)
	existing, err := r.ConfigGetAll(ctx, refspecKey)
	if err != nil {
		return result, err
	}
	if !containsString(existing, trackingRefspec) {
		if err := r.ConfigAdd(ctx, refspecKey, trackingRefspec); err != nil {
			return result, err
		}
		result.RefspecAdded = true
	}

	if err := r.ConfigSet(ctx, "core.logAllRefUpdates", "always"); err != nil {
		return result, err
	}
	result.LogAllRefUpdates = true

	made, err := ensureMetaConfig(ctx, dir, project)
	if err != nil {
		return result, err
	}
	result.MetaConfigMade = made

	return result, nil
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func ensureMetaConfig(ctx context.Context, dir, project string) (bool, error) {
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, refspace.MetaConfigRef)
	if err != nil {
		return false, err
	}
	if oid != "" {
		return false, nil // already initialized
	}

	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		return false, err
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return false, err
	}

	entityID, ts := idgen.NewWithTimestamp()
	eventID := idgen.New()
	data := map[string]any{
		"schema_version": SchemaVersion,
		"project":        project,
		"features":       toAnySlice(DefaultFeatures),
		"created_at":     ts,
	}
	env := &event.Envelope{
		V: 1, Kind: "meta.init", ID: eventID, TS: ts,
		Actor: sig.Email, Entity: entityID, Data: data, Extra: map[string]any{},
	}

	configJSON, err := event.Encode(data)
	if err != nil {
		return false, err
	}
	files := map[string][]byte{"config.json": configJSON}

	sig.When = time.Now()
	newHash, err := chain.AppendEvent(repo, chain.ZeroHash, env, "meta.init: "+project, files, sig)
	if err != nil {
		return false, err
	}
	if err := chain.AdvanceRef(dir, "refs/projects/meta/config", newHash, gitx.ZeroOID); err != nil {
		return false, err
	}
	return true, nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
