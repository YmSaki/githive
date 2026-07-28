package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

// newStatusCmd implements `githive status`: unpushed refs, unread notify
// count, and my doing tasks (docs/10-cli-spec.md「コマンド体系」).
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Summarize unpushed refs, unread notifications, and my doing tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()

			unpushed, err := syncapp.UnpushedRefs(ctx, dir)
			if err != nil {
				return err
			}

			sig, err := identity.Resolve(ctx, dir)
			if err != nil {
				return err
			}

			unread, err := notifyapp.New(dir).List(ctx, notifyapp.ListFilter{UnreadOnly: true, ActorEmail: sig.Email})
			if err != nil {
				return err
			}

			doing, err := taskapp.New(dir).List(ctx, taskapp.ListFilter{Status: "doing", Mine: true, ActorEmail: sig.Email})
			if err != nil {
				return err
			}

			if flags.json {
				doingItems := make([]any, len(doing))
				for i, m := range doing {
					doingItems[i] = m
				}
				cliout.PrintSuccess(map[string]any{
					"unpushed_refs":  unpushed,
					"unread_notify":  len(unread),
					"my_doing_tasks": doingItems,
				}, nil)
				return nil
			}

			fmt.Printf("unpushed refs: %d\n", len(unpushed))
			for _, ref := range unpushed {
				fmt.Println("  " + ref)
			}
			fmt.Printf("unread notifications: %d\n", len(unread))
			fmt.Printf("my doing tasks: %d\n", len(doing))
			for _, m := range doing {
				fmt.Printf("  %s  %v\n", shortID(m["id"]), m["title"])
			}
			return nil
		},
	}
}
