package event

import (
	"fmt"
	"regexp"
	"slices"
)

// InvalidInputError describes invalid data submitted to an app writer. It is
// separate from envelope decode errors: legacy events remain readable, while
// CLI/MCP callers can correct rejected input without appending an event.
type InvalidInputError struct {
	Kind  string
	Field string
	Rule  string
}

func (e *InvalidInputError) Error() string {
	return fmt.Sprintf("event: invalid input for %s.%s: %s", e.Kind, e.Field, e.Rule)
}

var dueDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// ValidateWriteData checks bounded issue/task/chat write constraints shared
// with spec/schemas. This is not a full JSON Schema validator and does not
// change decoding or fold: known but invalid transitions still get recorded.
func ValidateWriteData(e *Envelope) error {
	if err := validateUsersWriteData(e); err != nil {
		return err
	}
	if err := validateNotifyWriteData(e); err != nil {
		return err
	}
	requiredTitle := e.Kind == "issue.create" || e.Kind == "task.create" || e.Kind == "chat.create"
	optionalTitle := e.Kind == "issue.edit" || e.Kind == "task.edit" || e.Kind == "chat.edit_meta"
	if requiredTitle || optionalTitle {
		value, present := e.Data["title"]
		if requiredTitle || present {
			if title, ok := value.(string); !ok || title == "" {
				return &InvalidInputError{e.Kind, "title", "must be a non-empty string"}
			}
		}
	}
	var status []string
	switch e.Kind {
	case "issue.status":
		status = []string{"open", "in_progress", "resolved", "closed", "archived"}
	case "task.status":
		status = []string{"todo", "doing", "review", "done", "blocked", "cancelled"}
	case "chat.edit_meta":
		if err := writeEnum(e, "status", false, "open", "archived"); err != nil {
			return err
		}
	}
	if status != nil {
		if err := writeEnum(e, "to", true, status...); err != nil {
			return err
		}
		if err := writeEnum(e, "from", false, status...); err != nil {
			return err
		}
	}
	if e.Kind == "task.create" || e.Kind == "task.edit" {
		if err := writeEnum(e, "priority", false, "low", "normal", "high"); err != nil {
			return err
		}
		if value, present := e.Data["due"]; present {
			if due, ok := value.(string); !ok || !dueDatePattern.MatchString(due) {
				return &InvalidInputError{e.Kind, "due", "must match YYYY-MM-DD"}
			}
		}
	}
	if e.Kind == "issue.link" || e.Kind == "task.link" {
		if err := writeEnum(e, "rel", true, "task", "issue", "chat"); err != nil {
			return err
		}
		if err := writeULID(e, "id", true); err != nil {
			return err
		}
	}
	if e.Kind == "issue.comment" || e.Kind == "chat.post" {
		for _, field := range []string{"reply_to", "supersedes"} {
			if err := writeULID(e, field, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeEnum(e *Envelope, field string, required bool, allowed ...string) error {
	value, present := e.Data[field]
	if !present && !required {
		return nil
	}
	if text, ok := value.(string); !ok || !slices.Contains(allowed, text) {
		return &InvalidInputError{e.Kind, field, fmt.Sprintf("must be one of %v", allowed)}
	}
	return nil
}

func writeULID(e *Envelope, field string, required bool) error {
	value, present := e.Data[field]
	if !present && !required {
		return nil
	}
	if id, ok := value.(string); !ok || !IsValidULID(id) {
		return &InvalidInputError{e.Kind, field, "must be a full lowercase ULID"}
	}
	return nil
}
