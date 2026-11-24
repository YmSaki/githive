package chatapp

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/localcache"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ResolveID resolves a full or shortened (>=8 char) ULID to the full ID of
// exactly one existing chat thread.
func (s *Service) ResolveID(ctx context.Context, prefix string) (string, error) {
	id, err := idresolve.Entity(ctx, s.Dir, "chat", prefix)
	if err == idresolve.ErrNotFound {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Service) listIDs(ctx context.Context) ([]string, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/chat/")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureChat {
			continue
		}
		ids = append(ids, parsed.ID)
	}
	return ids, nil
}

// ListFilter narrows List results. Zero value means "no filter".
type ListFilter struct {
	Status string
}

// List returns a snapshot summary of every chat thread, using the fast
// snapshot-read path (meta.json at the ref head).
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Meta, error) {
	r := gitx.New(s.Dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/chat/")
	if err != nil {
		return nil, err
	}

	var cache *localcache.Cache
	var cached map[string]localcache.RefMetaEntry
	if c, err := localcache.Open(s.Dir); err == nil {
		cache = c
		defer cache.Close()
		cached, _ = cache.GetRefMetaByPrefix("refs/projects/chat/")
	}

	var repo *git.Repository
	var upserts []localcache.RefMetaEntry
	seen := make(map[string]bool, len(entries))

	var out []Meta
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != refspace.FeatureChat {
			continue
		}
		seen[e.Ref] = true
		var metaRaw []byte
		if c, ok := cached[e.Ref]; ok && c.HeadOID == e.OID {
			metaRaw = c.MetaJSON
		} else {
			if repo == nil {
				repo, err = chain.OpenRepository(s.Dir)
				if err != nil {
					return nil, err
				}
			}
			var ok bool
			metaRaw, ok, err = chain.ReadFile(repo, plumbing.NewHash(e.OID), "meta.json")
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if cache != nil {
				upserts = append(upserts, localcache.RefMetaEntry{
					RefName:  e.Ref,
					HeadOID:  e.OID,
					Feature:  "chat",
					EntityID: parsed.ID,
					MetaJSON: metaRaw,
				})
			}
		}
		decoded, err := event.DecodeGeneric(metaRaw)
		if err != nil {
			return nil, fmt.Errorf("chatapp: decode %s meta.json: %w", parsed.ID, err)
		}
		meta, ok := decoded.(map[string]any)
		if !ok {
			continue
		}
		if filter.Status != "" {
			if st, _ := meta["status"].(string); st != filter.Status {
				continue
			}
		}
		out = append(out, meta)
	}
	if cache != nil {
		var deletes []string
		for refName := range cached {
			if !seen[refName] {
				deletes = append(deletes, refName)
			}
		}
		_ = cache.SyncRefMeta(upserts, deletes)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["id"]) < fmt.Sprint(out[j]["id"])
	})
	return out, nil
}

// Show returns the full state of one chat thread (meta, messages).
func (s *Service) Show(ctx context.Context, id string) (*Show, error) {
	state, err := s.writer.Fold(ctx, id)
	if err != nil {
		return nil, err
	}
	return showState(state)
}

func showState(state *materialize.State) (*Show, error) {
	if state.Meta == nil {
		return nil, ErrNotFound
	}

	var messages []Message
	for _, mid := range sortedMessageIDs(state.Collections["messages"]) {
		if m, ok := state.Collections["messages"][mid].(map[string]any); ok {
			messages = append(messages, m)
		}
	}

	return &Show{Meta: state.Meta, Messages: messages}, nil
}
