package idresolve

import (
	"context"
	"sort"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// DisplayIDs chooses each entity's shortest unique prefix, at least eight
// characters long. Candidates include every same-feature local ref, including
// entities omitted by a list filter or archived state. No identities change.
func DisplayIDs(ctx context.Context, dir, feature string) (map[string]string, error) {
	kind := refspace.Feature(feature)
	if kind != refspace.FeatureIssue && kind != refspace.FeatureTask && kind != refspace.FeatureChat {
		return nil, &event.InvalidInputError{Kind: "reference", Field: "kind", Rule: "must be issue, task, or chat"}
	}
	entries, err := gitx.New(dir).ForEachRef(ctx, refspace.Root+"/"+feature+"/")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		parsed, err := refspace.Parse(entry.Ref)
		if err == nil && parsed.Feature == kind && parsed.ID != "" {
			ids = append(ids, parsed.ID)
		}
	}
	sort.Strings(ids)
	result := make(map[string]string, len(ids))
	for i, id := range ids {
		length := 8
		// In lexical order, the longest shared prefix occurs with a neighbor.
		for _, neighbor := range []int{i - 1, i + 1} {
			if neighbor < 0 || neighbor >= len(ids) {
				continue
			}
			common := 0
			for common < len(id) && common < len(ids[neighbor]) && id[common] == ids[neighbor][common] {
				common++
			}
			if common >= length {
				length = common + 1
			}
		}
		if length > len(id) {
			length = len(id)
		}
		result[id] = id[:length]
	}
	return result, nil
}
