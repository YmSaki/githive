package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

// errInvalidNotifyPriority is returned when --priority (CLI) or priority
// (MCP notify_post) is set to anything other than low/normal/high/empty
// (docs/features/notify.md「notify.post の data」「priority」). Shared by the
// CLI and the MCP server (mcp.go) via validateNotifyPriority so both reject
// the same invalid values; root.go's classifyError maps it to exit code 2
// (usage error, docs/10-cli-spec.md「終了コード」).
var errInvalidNotifyPriority = errors.New("--priority must be one of low, normal, high")

// validateNotifyPriority rejects any priority other than the three
// documented values; empty means "not set" and is always valid.
func validateNotifyPriority(priority string) error {
	switch priority {
	case "", "low", "normal", "high":
		return nil
	default:
		return fmt.Errorf("%w: got %q", errInvalidNotifyPriority, priority)
	}
}

func newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notify", Short: "Post and read notifications"}
	cmd.AddCommand(newNotifyPostCmd(), newNotifyListCmd(), newNotifyAckCmd())
	return cmd
}

func newNotifyPostCmd() *cobra.Command {
	var to []string
	var title, message, source, priority string
	cmd := &cobra.Command{
		Use:   "post",
		Short: "Post a notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			if err := validateNotifyPriority(priority); err != nil {
				return err
			}
			var sourceMap map[string]any
			if source != "" {
				kind, id, ok := strings.Cut(source, ":")
				if !ok {
					return fmt.Errorf("--source must be in kind:id form, got %q", source)
				}
				sourceMap = map[string]any{"kind": kind, "id": id}
			}
			ctx := context.Background()
			resolvedTo, err := resolveNotifyTargets(ctx, dir, to)
			if err != nil {
				return err
			}
			svc := notifyapp.New(dir)
			id, err := svc.Post(ctx, resolvedTo, title, message, sourceMap, priority)
			if err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, notifyapp.StreamRef)
			if flags.json {
				cliout.PrintSuccess(map[string]any{"id": id}, warnings)
				return nil
			}
			fmt.Println(id)
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", nil, "target(s): user:<email>, group:<name>, or all (repeatable)")
	cmd.Flags().StringVar(&title, "title", "", "notification title (required)")
	cmd.Flags().StringVarP(&message, "message", "m", "", "notification body")
	cmd.Flags().StringVar(&source, "source", "", "source entity as kind:id (e.g. task:01j8...)")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: low/normal/high")
	cmd.MarkFlagRequired("to")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newNotifyListCmd() *cobra.Command {
	var unread, all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List notifications",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			filter := notifyapp.ListFilter{UnreadOnly: unread && !all}
			if filter.UnreadOnly {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return err
				}
				filter.ActorEmail = sig.Email
			}
			svc := notifyapp.New(dir)
			items, err := svc.List(ctx, filter)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintList(items, nil)
				return nil
			}
			for _, m := range items {
				fmt.Printf("%-10s %-8v %v\n", shortID(m["id"]), m["targets"], m["title"])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&unread, "unread", false, "only unread notifications addressed to me")
	cmd.Flags().BoolVar(&all, "all", false, "list all notifications (overrides --unread)")
	return cmd
}

func newNotifyAckCmd() *cobra.Command {
	var ackAll bool
	cmd := &cobra.Command{
		Use:   "ack [event-id...]",
		Short: "Acknowledge notifications",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			svc := notifyapp.New(dir)
			ids := args
			if ackAll {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return err
				}
				unread, err := svc.List(ctx, notifyapp.ListFilter{UnreadOnly: true, ActorEmail: sig.Email})
				if err != nil {
					return err
				}
				ids = nil
				for _, m := range unread {
					if id, ok := m["id"].(string); ok {
						ids = append(ids, id)
					}
				}
			}
			if len(ids) == 0 {
				return fmt.Errorf("no event ids given (pass ids or --all)")
			}
			if err := svc.Ack(ctx, ids); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, notifyapp.StreamRef)
			if flags.json {
				cliout.PrintSuccess(map[string]any{"acked": ids}, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ackAll, "all", false, "acknowledge every unread notification addressed to me")
	return cmd
}
