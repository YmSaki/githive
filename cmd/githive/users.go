package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/event"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "users", Short: "Manage the users/groups/policy registry"}
	cmd.AddCommand(
		newUsersAddCmd(),
		newUsersListCmd(),
		newUsersKeyCmd(),
		newUsersGroupCmd(),
		newUsersPolicyCmd(),
	)
	return cmd
}

func newUsersAddCmd() *cobra.Command {
	var display, email, kind string
	var roles []string
	var agent bool
	var project, keyDir string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			username := args[0]
			if agent {
				setup, err := usersapp.AddAgent(ctx, dir, username, project, email, keyDir)
				if err != nil {
					return err
				}
				warnings := syncIfEnabled(dir, usersapp.RegistryRef)
				if flags.json {
					cliout.PrintSuccess(map[string]any{
						"username":   setup.Username,
						"email":      setup.Email,
						"public_key": setup.PublicKey,
						"pub_line":   setup.PubLine,
					}, warnings)
					return nil
				}
				fmt.Printf("created agent %s <%s>\n", setup.Username, setup.Email)
				fmt.Println(setup.ConfigSnippet)
				printWarnings(warnings)
				return nil
			}
			svc := usersapp.New(dir)
			if err := svc.AddUser(ctx, username, display, email, kind, roles); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&display, "display", "", "display name")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&kind, "kind", "", "human or agent")
	cmd.Flags().StringSliceVar(&roles, "role", nil, "roles to set")
	cmd.Flags().BoolVar(&agent, "agent", false, "set up a dedicated Agent identity (keypair + registry entry)")
	cmd.Flags().StringVar(&project, "project", "", "project name for the agent's minted email (default: read from meta/config)")
	cmd.Flags().StringVar(&keyDir, "key-dir", "", "directory to write the agent's SSH keypair into (default: ~/.ssh)")
	return cmd
}

func newUsersListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users, groups, and policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			users, groups, policy, err := svc.List(context.Background())
			if err != nil {
				return err
			}
			if flags.json {
				anyUsers := make([]any, len(users))
				for i, u := range users {
					anyUsers[i] = u
				}
				anyGroups := make([]any, len(groups))
				for i, g := range groups {
					anyGroups[i] = g
				}
				var policyAny any
				if policy != nil {
					policyAny = policy
				}
				cliout.PrintSuccess(map[string]any{"users": anyUsers, "groups": anyGroups, "policy": policyAny}, nil)
				return nil
			}
			for _, u := range users {
				fmt.Printf("user  %-20v %-8v %v\n", u["username"], u["kind"], u["emails"])
			}
			for _, g := range groups {
				fmt.Printf("group %-20v %v\n", g["name"], g["members"])
			}
			return nil
		},
	}
	return cmd
}

func newUsersKeyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage a user's SSH keys"}
	cmd.AddCommand(newUsersKeyAddCmd(), newUsersKeyRevokeCmd())
	return cmd
}

func newUsersKeyAddCmd() *cobra.Command {
	var pub string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register an SSH public key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			key, err := resolveKeyArg(pub)
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			ctx := context.Background()
			if err := svc.KeyAdd(ctx, args[0], key); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&pub, "pub", "", "public key (literal or path to a file, required)")
	cmd.MarkFlagRequired("pub")
	return cmd
}

func newUsersKeyRevokeCmd() *cobra.Command {
	var pub string
	cmd := &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke an SSH public key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			key, err := resolveKeyArg(pub)
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			ctx := context.Background()
			if err := svc.KeyRevoke(ctx, args[0], key); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&pub, "pub", "", "public key (literal or path to a file, required)")
	cmd.MarkFlagRequired("pub")
	return cmd
}

func newUsersGroupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "group", Short: "Manage groups"}
	cmd.AddCommand(newUsersGroupSetCmd(), newUsersGroupRemoveCmd())
	return cmd
}

func newUsersGroupSetCmd() *cobra.Command {
	var members []string
	var description string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or replace a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			ctx := context.Background()
			if err := svc.GroupSet(ctx, args[0], members, description); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&members, "member", nil, "group members (repeatable)")
	cmd.Flags().StringVar(&description, "description", "", "group description")
	return cmd
}

func newUsersGroupRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			ctx := context.Background()
			if err := svc.GroupRemove(ctx, args[0]); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	return cmd
}

func newUsersPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Manage the access policy"}
	cmd.AddCommand(newUsersPolicyEditCmd())
	return cmd
}

// newUsersPolicyEditCmd implements `githive users policy edit`
// (docs/features/users.md「policy は部分編集イベントにせず全置換とする」):
// dump the current policy to a temp file, launch $EDITOR, then publish the
// edited contents wholesale as users.policy_set.
func newUsersPolicyEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit policy.json in $EDITOR and publish it",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := usersapp.New(dir)
			ctx := context.Background()
			_, _, policy, err := svc.List(ctx)
			if err != nil {
				return err
			}
			if policy == nil {
				policy = map[string]any{"rules": []any{}, "default": "deny"}
			}
			before, err := event.Encode(policy)
			if err != nil {
				return err
			}
			tmp, err := os.CreateTemp("", "githive-policy-*.json")
			if err != nil {
				return err
			}
			tmpPath := tmp.Name()
			defer os.Remove(tmpPath)
			if _, err := tmp.Write(before); err != nil {
				tmp.Close()
				return err
			}
			if err := tmp.Close(); err != nil {
				return err
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			editCmd := exec.CommandContext(ctx, editor, tmpPath)
			editCmd.Stdin = os.Stdin
			editCmd.Stdout = os.Stdout
			editCmd.Stderr = os.Stderr
			if err := editCmd.Run(); err != nil {
				return fmt.Errorf("running editor %q: %w", editor, err)
			}
			after, err := os.ReadFile(tmpPath)
			if err != nil {
				return err
			}
			decoded, err := event.DecodeGeneric(after)
			if err != nil {
				return fmt.Errorf("invalid policy JSON: %w", err)
			}
			edited, ok := decoded.(map[string]any)
			if !ok {
				return fmt.Errorf("policy JSON must be an object")
			}
			rules, _ := edited["rules"].([]any)
			def, _ := edited["default"].(string)
			if err := svc.PolicySet(ctx, rules, def); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, usersapp.RegistryRef)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	return cmd
}

// resolveUserRefs resolves each of names (a username or an already
// fully-qualified email, ADR-0009) to an email via the users registry, for
// wiring into issue/task/notify flags that accept a person identifier.
func resolveUserRefs(ctx context.Context, dir string, names []string) ([]string, error) {
	if len(names) == 0 {
		return names, nil
	}
	svc := usersapp.New(dir)
	out := make([]string, len(names))
	for i, n := range names {
		email, err := svc.ResolveToEmail(ctx, n)
		if err != nil {
			return nil, err
		}
		out[i] = email
	}
	return out, nil
}

// resolveUserRef resolves a single username-or-email to an email.
func resolveUserRef(ctx context.Context, dir, name string) (string, error) {
	if name == "" {
		return name, nil
	}
	return usersapp.New(dir).ResolveToEmail(ctx, name)
}

// resolveNotifyTargets resolves the "user:"-prefixed entries of targets
// (a notify --to list) from username to email, leaving "group:" and "all"
// targets untouched (docs/features/notify.md's target addressing is
// email-based only for individual users).
func resolveNotifyTargets(ctx context.Context, dir string, targets []string) ([]string, error) {
	out := make([]string, len(targets))
	for i, t := range targets {
		name, ok := strings.CutPrefix(t, "user:")
		if !ok {
			out[i] = t
			continue
		}
		email, err := usersapp.New(dir).ResolveToEmail(ctx, name)
		if err != nil {
			return nil, err
		}
		out[i] = "user:" + email
	}
	return out, nil
}

// resolveKeyArg treats pub as a path to a readable file if one exists,
// otherwise as a literal public key string (docs/features/users.md「--pub
// <key or file>」).
func resolveKeyArg(pub string) (string, error) {
	if info, err := os.Stat(pub); err == nil && !info.IsDir() {
		content, err := readFile(pub)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(content), nil
	}
	return pub, nil
}
