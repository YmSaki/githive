// Package usersapp implements the users feature's application service:
// read/write operations over refs/projects/users/registry
// (docs/features/users.md).
package usersapp

import (
	"fmt"
	"regexp"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// usernameRe/groupnameRe match spec/schemas/users.schema.json exactly.
// Group names share the username namespace and thus its exact rule
// (docs/features/users.md「グループ名の文字集合はユーザー名と同じ規則」).
var (
	usernameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)
	groupnameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)
)

// ValidUsername reports whether name is a legal username.
func ValidUsername(name string) bool { return usernameRe.MatchString(name) }

// ValidGroupname reports whether name is a legal group name.
func ValidGroupname(name string) bool { return groupnameRe.MatchString(name) }

// TreeFiles renders a fold State into the users registry tree layout
// (docs/features/users.md「ref とツリー」): users/<name>.json,
// groups/<name>.json, and policy.json (only written once
// users.policy_set has been applied at least once).
func TreeFiles(state *materialize.State) (map[string][]byte, error) {
	files := map[string][]byte{}

	for username, raw := range state.Collections["users"] {
		userJSON, err := event.Encode(raw)
		if err != nil {
			return nil, fmt.Errorf("usersapp: encode users/%s.json: %w", username, err)
		}
		files["users/"+username+".json"] = userJSON
	}
	for name, raw := range state.Collections["groups"] {
		groupJSON, err := event.Encode(raw)
		if err != nil {
			return nil, fmt.Errorf("usersapp: encode groups/%s.json: %w", name, err)
		}
		files["groups/"+name+".json"] = groupJSON
	}
	if policy := materialize.Policy(state); policy != nil {
		policyJSON, err := event.Encode(policy)
		if err != nil {
			return nil, fmt.Errorf("usersapp: encode policy.json: %w", err)
		}
		files["policy.json"] = policyJSON
	}
	return files, nil
}
