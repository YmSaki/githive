package event

import (
	"fmt"
	"regexp"
	"slices"
)

var storedNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)
var notifyTargetPattern = regexp.MustCompile(`^(user:[^\s@]+@[^\s]+|group:[a-z0-9][a-z0-9-]{1,38}|all)$`)

// UnsafeStoredDataError reports a registry name that cannot safely be used as
// a materialized tree path. Readers must reject it, never silently drop it.
type UnsafeStoredDataError struct {
	Kind  string
	Field string
}

func (e *UnsafeStoredDataError) Error() string {
	return fmt.Sprintf("event: unsafe stored registry name in %s.%s", e.Kind, e.Field)
}

// ValidateStoredData is a narrow safety check at the chain read boundary. It
// does not apply new write rules to legacy fields or change reducer semantics.
func ValidateStoredData(e *Envelope) error {
	field := ""
	switch e.Kind {
	case "users.user_set", "users.key_add", "users.key_revoke":
		field = "username"
	case "users.group_set", "users.group_remove":
		field = "name"
	default:
		return nil
	}
	name, ok := e.Data[field].(string)
	if !ok || !storedNamePattern.MatchString(name) {
		return &UnsafeStoredDataError{e.Kind, field}
	}
	return nil
}

func validateUsersWriteData(e *Envelope) error {
	if err := ValidateStoredData(e); err != nil {
		unsafe := err.(*UnsafeStoredDataError)
		return &InvalidInputError{e.Kind, unsafe.Field, "must match [a-z0-9][a-z0-9-]{1,38}"}
	}
	if e.Kind == "users.user_set" {
		fields, ok := e.Data["fields"].(map[string]any)
		if !ok {
			return &InvalidInputError{e.Kind, "fields", "must be an object"}
		}
		for _, rule := range []struct {
			field   string
			allowed []string
		}{
			{"kind", []string{"human", "agent"}},
			{"status", []string{"active", "suspended"}},
		} {
			if value, present := fields[rule.field]; present {
				if text, ok := value.(string); !ok || !slices.Contains(rule.allowed, text) {
					return &InvalidInputError{e.Kind, "fields." + rule.field, fmt.Sprintf("must be one of %v", rule.allowed)}
				}
			}
		}
	}
	if e.Kind == "users.group_set" {
		members, ok := writeStrings(e.Data["members"])
		if !ok {
			return &InvalidInputError{e.Kind, "members", "must be an array of usernames"}
		}
		for _, name := range members {
			if !storedNamePattern.MatchString(name) {
				return &InvalidInputError{e.Kind, "members", "must contain valid usernames"}
			}
		}
	}
	return nil
}

func validateNotifyWriteData(e *Envelope) error {
	if e.Kind != "notify.post" {
		return nil
	}
	title, ok := e.Data["title"].(string)
	if !ok || title == "" {
		return &InvalidInputError{e.Kind, "title", "must be a non-empty string"}
	}
	targets, ok := writeStrings(e.Data["targets"])
	if !ok || len(targets) == 0 {
		return &InvalidInputError{e.Kind, "targets", "must be a non-empty array of targets"}
	}
	for _, target := range targets {
		if !notifyTargetPattern.MatchString(target) {
			return &InvalidInputError{e.Kind, "targets", "must use user:<email>, group:<name>, or all"}
		}
	}
	if err := writeEnum(e, "priority", false, "low", "normal", "high"); err != nil {
		return err
	}
	if value, present := e.Data["source"]; present {
		source, ok := value.(map[string]any)
		if !ok {
			return &InvalidInputError{e.Kind, "source", "must be an object"}
		}
		if _, ok := source["kind"].(string); !ok {
			return &InvalidInputError{e.Kind, "source.kind", "must be a string"}
		}
		if id, ok := source["id"].(string); !ok || !IsValidULID(id) {
			return &InvalidInputError{e.Kind, "source.id", "must be a full lowercase ULID"}
		}
	}
	return nil
}

func writeStrings(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return values, true
	case []any:
		out := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			out[i] = text
		}
		return out, true
	default:
		return nil, false
	}
}
