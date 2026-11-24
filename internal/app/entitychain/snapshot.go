package entitychain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/localcache"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ListSnapshotMetas reads meta.json at the head of every ref matching feature
// under prefix, using .git/githive-cache.db when available and falling back
// transparently to Git on cache miss, invalid cached payload, or cache error.
func ListSnapshotMetas(ctx context.Context, dir, prefix string, feature refspace.Feature) ([]map[string]any, error) {
	r := gitx.New(dir)
	entries, err := r.ForEachRef(ctx, prefix)
	if err != nil {
		return nil, err
	}

	var cache *localcache.Cache
	var cached map[string]localcache.RefMetaEntry
	if c, err := localcache.Open(dir); err == nil {
		cache = c
		defer cache.Close()
		cached, _ = cache.GetRefMetaByPrefix(prefix)
	}

	var repo *git.Repository
	var upserts []localcache.RefMetaEntry
	seen := make(map[string]bool, len(entries))
	out := make([]map[string]any, 0, len(entries))

	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil || parsed.Feature != feature {
			continue
		}
		seen[e.Ref] = true

		var meta map[string]any
		if c, ok := cached[e.Ref]; ok && c.HeadOID == e.OID && c.MetaSHA256 != "" && c.MetaBlobOID != "" && c.MetaSHA256 == sha256Hex(c.MetaJSON) {
			if repo == nil {
				repo, err = chain.OpenRepository(dir)
				if err != nil {
					return nil, err
				}
			}
			entryHash, found, err := chain.ReadFileEntryHash(repo, plumbing.NewHash(e.OID), "meta.json")
			if err != nil {
				return nil, err
			}
			if found && entryHash.String() == c.MetaBlobOID && plumbing.ComputeHash(plumbing.BlobObject, c.MetaJSON) == entryHash {
				if decoded, err := event.DecodeGeneric(c.MetaJSON); err == nil {
					if m, ok := decoded.(map[string]any); ok {
						meta = m
					}
				}
			}
		}
		if meta == nil {
			if repo == nil {
				repo, err = chain.OpenRepository(dir)
				if err != nil {
					return nil, err
				}
			}
			metaRaw, blobHash, ok, err := chain.ReadFileWithBlobHash(repo, plumbing.NewHash(e.OID), "meta.json")
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			decoded, err := event.DecodeGeneric(metaRaw)
			if err != nil {
				return nil, fmt.Errorf("%s: decode %s meta.json: %w", feature, parsed.ID, err)
			}
			m, ok := decoded.(map[string]any)
			if !ok {
				continue
			}
			meta = m
			if cache != nil {
				upserts = append(upserts, localcache.RefMetaEntry{
					RefName:     e.Ref,
					HeadOID:     e.OID,
					Feature:     string(feature),
					EntityID:    parsed.ID,
					MetaJSON:    metaRaw,
					MetaSHA256:  sha256Hex(metaRaw),
					MetaBlobOID: blobHash.String(),
				})
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

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
