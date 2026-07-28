package usersapp

import (
	"context"
	"fmt"
)

// MentionTargets resolves an @name from a chat body into notify targets
// (docs/features/chat.md「メンションと通知」).
//
// A username resolves to that user's first registered email; a group name
// resolves to one target per member, each resolved the same way. A name the
// registry knows as neither is reported as an error, which callers treat as
// "this @word was not a mention" - chat bodies are full of @words that are
// not.
//
// Group membership is read here, at post time, and the resulting target list
// is what gets notified. chat.md calls for exactly that snapshot: adding
// someone to the group later does not retroactively notify them about an
// older message.
func MentionTargets(ctx context.Context, dir, name string) ([]string, error) {
	svc := New(dir)

	if email, err := svc.ResolveToEmail(ctx, name); err == nil && email != "" {
		return []string{"user:" + email}, nil
	}

	_, groups, _, err := svc.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if gname, _ := g["name"].(string); gname != name {
			continue
		}
		var targets []string
		for _, member := range asStringSlice(g["members"]) {
			email, err := svc.ResolveToEmail(ctx, member)
			if err != nil || email == "" {
				continue // a member the registry no longer knows
			}
			targets = append(targets, "user:"+email)
		}
		return targets, nil
	}
	return nil, fmt.Errorf("%w: %q is neither a user nor a group", ErrUserNotFound, name)
}
