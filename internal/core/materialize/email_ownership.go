package materialize

import "sort"

// EmailConflict identifies an email claimed by multiple registry users.
type EmailConflict struct {
	Email     string
	Usernames []string
}

// EmailOwners maps each exact email to its sorted, distinct username owners.
// Multiple aliases or repeated entries within one user are not conflicts.
func EmailOwners(s *State) map[string][]string {
	owners := map[string][]string{}
	for name, raw := range s.Collections["users"] {
		user, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, email := range asStringSlice(user["emails"]) {
			if email != "" && !seen[email] {
				owners[email] = append(owners[email], name)
				seen[email] = true
			}
		}
	}
	for _, names := range owners {
		sort.Strings(names)
	}
	return owners
}

// EmailConflicts returns ambiguous emails in deterministic order.
func EmailConflicts(s *State) []EmailConflict {
	var out []EmailConflict
	for email, names := range EmailOwners(s) {
		if len(names) > 1 {
			out = append(out, EmailConflict{Email: email, Usernames: names})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

func uniqueUserForEmail(s *State, email string) map[string]any {
	owners := EmailOwners(s)[email]
	if len(owners) != 1 {
		return nil
	}
	user, _ := s.Collections["users"][owners[0]].(map[string]any)
	return user
}
