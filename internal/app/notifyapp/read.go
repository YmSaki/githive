package notifyapp

import (
	"context"
	"slices"
	"sort"

	"github.com/ymsaki/githive/internal/core/materialize"
)

// MembershipResolver returns, for the reading actor's email, every email of
// the same registry user and the groups that user belongs to
// (docs/features/notify.md「未読の判定」: 直接指定、所属グループ、all). It is
// injected like MentionResolver so notifyapp does not depend on the users
// registry, and it is applied only when reading: group expansion never
// reaches fold, keeping materialization independent of another ref
// (docs/02-data-model.md「決定性の不変条件」).
type MembershipResolver func(ctx context.Context, email string) (emails, groups []string, err error)

// ListFilter narrows List results.
type ListFilter struct {
	// UnreadOnly, when true, restricts to posts addressed to ActorEmail -
	// via "all", "user:<email>" for any of the actor's emails, or
	// "group:<name>" for any group the actor belongs to - that the actor
	// has not acked yet (docs/features/notify.md「未読の判定」).
	UnreadOnly bool
	ActorEmail string
	// Membership resolves the actor's emails and groups. nil means only
	// ActorEmail itself, with no groups.
	Membership MembershipResolver
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

	var self map[string]bool // targets that address the actor
	var selfEmails []string  // acks by any of these count as the actor's
	if filter.UnreadOnly {
		selfEmails = []string{filter.ActorEmail}
		var groups []string
		if filter.Membership != nil {
			emails, gs, err := filter.Membership(ctx, filter.ActorEmail)
			if err != nil {
				return nil, err
			}
			if len(emails) > 0 {
				selfEmails = emails
			}
			groups = gs
		}
		self = map[string]bool{"all": true}
		for _, e := range selfEmails {
			self["user:"+e] = true
		}
		for _, g := range groups {
			self["group:"+g] = true
		}
	}

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
			if !addressedTo(post, self) {
				continue
			}
			if hasAcked(acks[id], selfEmails) {
				continue
			}
		}
		out = append(out, post)
	}
	return out, nil
}

func addressedTo(post map[string]any, self map[string]bool) bool {
	targets, _ := post["targets"].([]any)
	for _, t := range targets {
		if target, ok := t.(string); ok && self[target] {
			return true
		}
	}
	return false
}

func hasAcked(actors []string, selfEmails []string) bool {
	for _, a := range actors {
		if slices.Contains(selfEmails, a) {
			return true
		}
	}
	return false
}
