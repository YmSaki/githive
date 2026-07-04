package notifyapp

import (
	"context"
	"sort"

	"github.com/ymsaki/githive/internal/core/materialize"
)

// ListFilter narrows List results.
type ListFilter struct {
	// UnreadOnly, when true, restricts to posts addressed to ActorEmail
	// (directly via "user:<email>" or via "all") that ActorEmail has not
	// acked yet (docs/features/notify.md「未読の判定」).
	//
	// Group targets ("group:<name>") cannot be resolved yet: that requires
	// the users registry, which lands in P3
	// (docs/13-roadmap.md「P3：users / 署名 / verify」). Until then, group
	// membership is not considered when computing "addressed to me".
	UnreadOnly bool
	ActorEmail string
}

// List returns every post (or, with UnreadOnly, only this actor's unread
// posts), sorted by event ID (chronological, since IDs are ULIDs).
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Post, error) {
	state, err := s.writer.Fold(ctx, "")
	if err != nil {
		return nil, err
	}
	if state.Meta == nil {
		return nil, nil
	}
	acks := materialize.NotifyAcks(state)

	ids := make([]string, 0, len(state.Collections["posts"]))
	for id := range state.Collections["posts"] {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []Post
	for _, id := range ids {
		post, ok := state.Collections["posts"][id].(map[string]any)
		if !ok {
			continue
		}
		if filter.UnreadOnly {
			if !addressedTo(post, filter.ActorEmail) {
				continue
			}
			if hasAcked(acks[id], filter.ActorEmail) {
				continue
			}
		}
		out = append(out, post)
	}
	return out, nil
}

func addressedTo(post map[string]any, actorEmail string) bool {
	targets, _ := post["targets"].([]any)
	for _, t := range targets {
		target, ok := t.(string)
		if !ok {
			continue
		}
		if target == "all" || target == "user:"+actorEmail {
			return true
		}
	}
	return false
}

func hasAcked(actors []string, actorEmail string) bool {
	for _, a := range actors {
		if a == actorEmail {
			return true
		}
	}
	return false
}
