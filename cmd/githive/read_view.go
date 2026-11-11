package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
)

func viewData(data map[string]any, source string) map[string]any {
	if source != "" {
		data["read_source"] = "snapshot"
		data["history_complete"] = false
		data["verified"] = false
	}
	return data
}

// resolveUserRefView is only for display filters. It is deliberately separate
// from mutation target resolution, which still requires complete registry history.
func resolveUserRefView(ctx context.Context, dir, name string) (string, string, error) {
	if name == "" || strings.Contains(name, "@") {
		return name, "", nil
	}
	users, _, _, source, err := usersapp.New(dir).ListView(ctx)
	if err != nil {
		return "", "", err
	}
	for _, user := range users {
		if user["username"] == name {
			emails := asAnyStrings(user["emails"])
			if len(emails) > 0 {
				return emails[0], source, nil
			}
		}
	}
	return name, source, nil
}

func viewList(items []map[string]any, source string) map[string]any {
	values := make([]any, len(items))
	for i, item := range items {
		values[i] = item
	}
	return viewData(map[string]any{"items": values, "total": len(values)}, source)
}

func printViewSource(source string) {
	if source != "" && !flags.json {
		fmt.Fprintln(os.Stderr, "warning: snapshot view; history incomplete and unverified; Markdown bodies are persisted renderings (including their trailing LF)")
	}
}

func viewMembershipResolver(dir string, source *string) notifyapp.MembershipResolver {
	return func(ctx context.Context, email string) ([]string, []string, error) {
		emails, groups, readSource, err := usersapp.MembershipView(ctx, dir, email)
		if readSource != "" {
			*source = readSource
		}
		return emails, groups, err
	}
}
