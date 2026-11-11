package event

import (
	"fmt"
	"strings"
)

// validateUsersKeyOrder rejects key operations that the unchanged reducer
// would ignore, and aliases that could leave another copy of a key active.
// Key identity is the existing users API's type + base64 token, not a new
// cryptographic parser. Full history is required; snapshots are not evidence.
func validateUsersKeyOrder(events []*Envelope) error {
	var inputs []*Envelope
	for _, env := range events {
		switch env.Kind {
		case "users.user_set", "users.key_add", "users.key_revoke":
			inputs = append(inputs, env)
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	users := map[string]map[string]string{} // username -> key material -> exact pub
	for _, env := range SortEvents(inputs) {
		username, _ := env.Data["username"].(string)
		switch env.Kind {
		case "users.user_set":
			if username != "" && users[username] == nil {
				users[username] = map[string]string{}
			}
		case "users.key_add", "users.key_revoke":
			violation := func(rule string) error {
				return fmt.Errorf("%w: %s event %s for user %q: %s; inspect the original registry history before repairing it", ErrEventOrderViolation, env.Kind, env.ID, username, rule)
			}
			keys := users[username]
			if keys == nil {
				return violation("must follow user_set in event ID order")
			}
			pub, ok := env.Data["pub"].(string)
			fields := strings.Fields(pub)
			if !ok || len(fields) == 0 {
				return violation("pub must be a non-empty key identity")
			}
			material := fields[0]
			if len(fields) > 1 {
				material += " " + fields[1]
			}
			registered, exists := keys[material]
			if env.Kind == "users.key_add" {
				if exists && registered != pub {
					return violation("same key material was already registered with a different pub string")
				}
				keys[material] = pub
				continue
			}
			if !exists {
				return violation("must follow key_add for the same user's key material")
			}
			if registered != pub {
				return violation("revoke pub must exactly match the registered pub; comment or whitespace differences would be ignored")
			}
			// Keep the registration after revocation. Repeated exact revoke
			// and add events remain idempotent; the reducer never reactivates it.
		}
	}
	return nil
}
