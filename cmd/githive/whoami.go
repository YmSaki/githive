package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the identity (git user.email) githive will record as actor",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			sig, err := identity.Resolve(ctx, dir)
			if err != nil {
				return err
			}

			user, keys := matchRegistryUser(ctx, dir, sig.Email)

			if flags.json {
				data := map[string]any{"name": sig.Name, "email": sig.Email}
				if user != nil {
					data["username"] = user["username"]
					data["registered"] = true
					data["keys"] = keys
				} else {
					data["registered"] = false
				}
				cliout.PrintSuccess(data, nil)
				return nil
			}

			fmt.Printf("%s <%s>\n", sig.Name, sig.Email)
			if user == nil {
				fmt.Println("not registered in refs/projects/users/registry")
				return nil
			}
			fmt.Printf("registry user: %v\n", user["username"])
			if len(keys) == 0 {
				fmt.Println("no SSH keys registered")
			}
			for _, k := range keys {
				status := "active"
				if k["revoked_at"] != nil {
					status = fmt.Sprintf("revoked at %v", k["revoked_at"])
				}
				fmt.Printf("  key: %v (%s)\n", k["pub"], status)
			}
			return nil
		},
	}
}

// matchRegistryUser finds the registry user (if any) whose emails include
// email, and returns its raw key list.
func matchRegistryUser(ctx context.Context, dir, email string) (usersapp.User, []map[string]any) {
	svc := usersapp.New(dir)
	users, _, _, err := svc.List(ctx)
	if err != nil {
		return nil, nil
	}
	for _, u := range users {
		for _, e := range asAnyStrings(u["emails"]) {
			if e == email {
				return u, asAnyMaps(u["keys"])
			}
		}
	}
	return nil, nil
}

func asAnyStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asAnyMaps(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
