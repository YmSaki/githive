package usersapp

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/ymsaki/githive/internal/app/snapshotread"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// ListView reads display-only registry snapshots. List/GetUser/ResolveToEmail
// retain complete-history semantics for mutation and authority decisions.
func (s *Service) ListView(ctx context.Context) (users []User, groups []Group, policy map[string]any, source string, err error) {
	users, groups, policy, err = s.List(ctx)
	var shallow *chain.ShallowHistoryError
	if !errors.As(err, &shallow) {
		return users, groups, policy, "", err
	}
	state, err := snapshotread.Read(ctx, s.Dir, RegistryRef, "users")
	if err != nil {
		return nil, nil, nil, "", err
	}
	for _, raw := range state.Collections["users"] {
		u, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, "", errors.New("snapshot: invalid user")
		}
		users = append(users, u)
	}
	for _, raw := range state.Collections["groups"] {
		g, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, "", errors.New("snapshot: invalid group")
		}
		groups = append(groups, g)
	}
	sortRegistryLists(users, groups)
	return users, groups, materialize.Policy(state), "snapshot", nil
}

// MembershipView is only for read-only unread counts, never posting targets.
func MembershipView(ctx context.Context, dir, email string) (emails, groups []string, source string, err error) {
	users, groupList, _, source, err := New(dir).ListView(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	emails = []string{email}
	ids := map[string]bool{email: true}
	for _, u := range users {
		es := asStringSlice(u["emails"])
		if !slices.Contains(es, email) {
			continue
		}
		if name, ok := u["username"].(string); ok {
			ids[name] = true
		}
		for _, e := range es {
			if !ids[e] {
				ids[e] = true
				emails = append(emails, e)
			}
		}
	}
	for _, g := range groupList {
		for _, member := range asStringSlice(g["members"]) {
			if ids[member] {
				groups = append(groups, g["name"].(string))
				break
			}
		}
	}
	sort.Strings(groups)
	return emails, groups, source, nil
}
