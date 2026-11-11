// Package initapp implements `githive init`: it configures the remote
// tracking refspec and core.logAllRefUpdates that keep local writes safe
// from IDE auto-fetch (docs/02-data-model.md「ローカル作業名前空間とリモート
// 追跡の分離」, ADR-0008), and creates refs/projects/meta/config if it does
// not exist yet (docs/02-data-model.md「meta/config ref」).
package initapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/sign"
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
	// Remote is what Init learned about the sync remote.
	Remote RemoteStatus
	// Warnings are non-fatal conditions the caller should surface (e.g. no
	// remote configured, so nothing was fetched).
	Warnings []string
}

// RemoteStatus describes the remote Init fetched from.
type RemoteStatus struct {
	Name string
	// Configured is false when remote.<name>.url is unset; Init then neither
	// adds the tracking refspec nor fetches.
	Configured bool
	Fetched    bool
	// HasMetaConfig reports whether the remote already had
	// refs/projects/meta/config, i.e. this clone joined an existing project.
	HasMetaConfig bool
	// AdoptedRefs are the local refs Init created from the remote because
	// the clone did not have them yet (meta/config included).
	AdoptedRefs []string
}

// Init is idempotent: running it again on an already-initialized repo is a
// no-op (docs/10-cli-spec.md: `githive init` # fetch refspec 追加、meta/config
// 作成（無ければ）、初回 fetch).
//
// Order matters (issue #101):
//  1. identity is checked before anything is written, so a failing init
//     leaves the repository untouched;
//  2. the tracking refspec is only added for a remote that exists - a
//     url-less remote.<name>.fetch would make a later `git remote add` fail;
//  3. the remote's refs/projects/ namespace is fetched, and refs this clone
//     lacks are created from it, before deciding whether meta/config needs
//     creating. A clone joining an existing project therefore adopts the
//     project's meta/config rather than writing a second meta.init root
//     that sync would later merge over the shared project name.
func Init(ctx context.Context, dir, remote, project string) (Result, error) {
	result := Result{Remote: RemoteStatus{Name: remote}}
	r := gitx.New(dir)

	if _, err := identity.Resolve(ctx, dir); err != nil {
		return result, err
	}

	url, err := r.ConfigGet(ctx, fmt.Sprintf("remote.%s.url", remote))
	if err != nil {
		return result, err
	}
	result.Remote.Configured = url != ""

	if result.Remote.Configured {
		refspecKey := fmt.Sprintf("remote.%s.fetch", remote)
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
	} else {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"remote %q is not configured; githive refs will not be fetched or synced. Add the remote and run `githive init` again to set it up", remote))
	}

	if err := r.ConfigSet(ctx, "core.logAllRefUpdates", "always"); err != nil {
		return result, err
	}
	result.LogAllRefUpdates = true

	if result.Remote.Configured {
		if err := r.FetchPruned(ctx, remote, trackingRefspec); err != nil {
			local, revErr := r.RevParse(ctx, refspace.MetaConfigRef)
			if revErr != nil {
				return result, revErr
			}
			if local == "" {
				// Creating meta/config without knowing whether the remote
				// already has one is exactly how a second root gets made,
				// so a failed fetch stops init instead of guessing.
				return result, fmt.Errorf("initapp: initial fetch from %q failed, so init cannot tell whether the project already exists there (run `githive init` again once the remote is reachable): %w", remote, err)
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("fetch from %q failed: %v", remote, err))
		} else {
			result.Remote.Fetched = true
		}
	}
	if result.Remote.Fetched {
		adopted, err := adoptRemoteOnlyRefs(ctx, r)
		if err != nil {
			return result, err
		}
		result.Remote.AdoptedRefs = adopted
		tracking, err := refspace.RemoteTrackingRef(refspace.MetaConfigRef)
		if err != nil {
			return result, err
		}
		oid, err := r.RevParse(ctx, tracking)
		if err != nil {
			return result, err
		}
		result.Remote.HasMetaConfig = oid != ""
	}

	made, err := ensureMetaConfig(ctx, dir, project)
	if err != nil {
		return result, err
	}
	result.MetaConfigMade = made

	return result, nil
}

// trackingRefspec maps the remote's refs/projects/ namespace into the
// separate tracking namespace (ADR-0008).
var trackingRefspec = fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)

// adoptRemoteOnlyRefs creates every local refs/projects/ ref that exists on
// the remote but not yet locally, pointing it at the fetched commit. That is
// the fast-forward sync would do for a remote-only ref; doing it here makes
// a fresh clone readable right after init. Refs that already exist locally
// are left for sync, which knows how to merge them.
func adoptRemoteOnlyRefs(ctx context.Context, r *gitx.Runner) ([]string, error) {
	entries, err := r.ForEachRef(ctx, refspace.RemoteTrackingRoot+"/")
	if err != nil {
		return nil, err
	}
	var adopted []string
	for _, e := range entries {
		local, err := refspace.LocalRefFromTracking(e.Ref)
		if err != nil {
			continue
		}
		oid, err := r.RevParse(ctx, local)
		if err != nil {
			return adopted, err
		}
		if oid != "" {
			continue
		}
		if local == refspace.UsersRegistryRef {
			repo, err := chain.OpenRepository(r.Dir)
			if err != nil {
				return adopted, err
			}
			if _, err := chain.WalkChain(repo, plumbing.NewHash(e.OID)); err != nil {
				return adopted, fmt.Errorf("initapp: refusing remote registry: %w", err)
			}
		}
		if err := r.UpdateRef(ctx, local, e.OID, gitx.ZeroOID); err != nil {
			if errors.Is(err, gitx.ErrRefCASMismatch) {
				continue // created concurrently; sync reconciles it
			}
			return adopted, err
		}
		adopted = append(adopted, local)
	}
	return adopted, nil
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
		V: event.SchemaVersion, Kind: "meta.init", ID: eventID, TS: ts,
		Actor: sig.Email, Entity: entityID, Data: data, Extra: map[string]any{},
	}

	configJSON, err := event.Encode(data)
	if err != nil {
		return false, err
	}
	files := map[string][]byte{"config.json": configJSON}

	signer, cleanupSigner, err := sign.ResolveSigner(ctx, dir)
	if err != nil {
		return false, err
	}
	defer cleanupSigner()

	sig.When = time.Now()
	newHash, err := chain.AppendEvent(repo, chain.ZeroHash, env, "meta.init: "+project, files, sig, chain.WithSigner(signer))
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
