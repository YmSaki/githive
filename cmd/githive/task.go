package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Manage tasks"}
	cmd.AddCommand(
		newTaskNewCmd(),
		newTaskListCmd(),
		newTaskShowCmd(),
		newTaskStatusCmd(),
		newTaskNoteCmd(),
		newTaskReassignCmd(),
	)
	return cmd
}

func taskRef(id string) string { return "refs/projects/task/" + id }

func resolveTaskID(ctx context.Context, svc *taskapp.Service, arg string) (string, error) {
	return svc.ResolveID(ctx, arg)
}

func newTaskNewCmd() *cobra.Command {
	var title, body, owner, due, priority string
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new task",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			resolvedOwner, err := resolveUserRef(ctx, dir, owner)
			if err != nil {
				return err
			}
			svc := taskapp.New(dir)
			id, err := svc.NewTask(ctx, title, body, resolvedOwner, due, priority)
			if err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, taskRef(id))
			if flags.json {
				cliout.PrintSuccess(map[string]any{"id": id}, warnings)
				return nil
			}
			fmt.Println(id)
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&owner, "owner", "", "owner (defaults to actor)")
	cmd.Flags().StringVar(&due, "due", "", "due date")
	cmd.Flags().StringVar(&priority, "priority", "", "priority")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var status, owner string
	var mine bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			filter := taskapp.ListFilter{Status: status, Owner: owner, Mine: mine}
			if mine {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return err
				}
				filter.ActorEmail = sig.Email
			}
			svc := taskapp.New(dir)
			items, err := svc.List(ctx, filter)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintList(items, nil)
				return nil
			}
			fmt.Printf("%-10s %-10s %-40s %s\n", "ID", "STATUS", "TITLE", "OWNER")
			for _, m := range items {
				fmt.Printf("%-10s %-10v %-40v %v\n", shortID(m["id"]), m["status"], m["title"], m["owner"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&owner, "owner", "", "filter by owner")
	cmd.Flags().BoolVar(&mine, "mine", false, "only tasks owned by me")
	return cmd
}

func newTaskShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := taskapp.New(dir)
			ctx := context.Background()
			id, err := resolveTaskID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return err
			}
			if flags.json {
				notes := make([]any, len(show.Notes))
				for i, n := range show.Notes {
					notes[i] = n
				}
				cliout.PrintSuccess(map[string]any{"meta": show.Meta, "body": show.Body, "notes": notes}, nil)
				return nil
			}
			fmt.Printf("%v  %v  %v  owner=%v\n", show.Meta["id"], show.Meta["status"], show.Meta["title"], show.Meta["owner"])
			if show.Body != "" {
				fmt.Println()
				fmt.Println(show.Body)
			}
			for _, n := range show.Notes {
				fmt.Printf("\n--- %v (%v) ---\n%v\n", n["author"], n["ts"], n["body"])
			}
			return nil
		},
	}
	return cmd
}

func newTaskStatusCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "status <id> <to>",
		Short: "Transition a task's status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := taskapp.New(dir)
			ctx := context.Background()
			id, err := resolveTaskID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			ok, err := svc.Status(ctx, id, args[1], note)
			if err != nil {
				return err
			}
			var warnings []cliout.Warning
			if !ok {
				warnings = append(warnings, cliout.Warning{Code: "invalid_transition", Message: fmt.Sprintf("transition to %q was rejected", args[1])})
			} else if args[1] == "done" {
				// Notify the task's creator (docs/features/notify.md「自動通知」).
				if show, showErr := svc.Show(ctx, id); showErr == nil {
					createdBy, _ := show.Meta["created_by"].(string)
					sig, sigErr := identity.Resolve(ctx, dir)
					if createdBy != "" && (sigErr != nil || createdBy != sig.Email) {
						for _, w := range autoNotify(ctx, dir, "user:"+createdBy,
							fmt.Sprintf("task %s が done になりました", shortID(id)),
							map[string]any{"kind": "task", "id": id}) {
							warnings = append(warnings, cliout.Warning{Code: "auto_notify_failed", Message: w})
						}
					}
				}
			}
			warnings = append(warnings, syncIfEnabled(dir, taskRef(id))...)
			if flags.json {
				cliout.PrintSuccess(map[string]any{"applied": ok}, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVarP(&note, "message", "m", "", "note to attach to this transition")
	return cmd
}

func newTaskNoteCmd() *cobra.Command {
	var message, file string
	cmd := &cobra.Command{
		Use:   "note <id>",
		Short: "Add a note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			if file != "" {
				content, err := readFile(file)
				if err != nil {
					return err
				}
				message = content
			}
			svc := taskapp.New(dir)
			ctx := context.Background()
			id, err := resolveTaskID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Note(ctx, id, message); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, taskRef(id))
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "note body")
	cmd.Flags().StringVarP(&file, "file", "F", "", "read note body from file")
	return cmd
}

func newTaskReassignCmd() *cobra.Command {
	var owner string
	cmd := &cobra.Command{
		Use:   "reassign <id>",
		Short: "Reassign a task's owner",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := taskapp.New(dir)
			ctx := context.Background()
			id, err := resolveTaskID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			resolvedOwner, err := resolveUserRef(ctx, dir, owner)
			if err != nil {
				return err
			}

			before, showErr := svc.Show(ctx, id)
			previousOwner := ""
			if showErr == nil {
				previousOwner, _ = before.Meta["owner"].(string)
			}

			if err := svc.Reassign(ctx, id, resolvedOwner); err != nil {
				return err
			}

			var warnings []cliout.Warning
			selfEmail := ""
			if sig, sigErr := identity.Resolve(ctx, dir); sigErr == nil {
				selfEmail = sig.Email
			}
			if resolvedOwner != selfEmail && resolvedOwner != previousOwner {
				// Don't notify yourself, and don't re-notify the owner
				// this task already had (docs/features/notify.md「自動通知」).
				for _, w := range autoNotify(ctx, dir, "user:"+resolvedOwner,
					fmt.Sprintf("task %s の担当になりました", shortID(id)),
					map[string]any{"kind": "task", "id": id}) {
					warnings = append(warnings, cliout.Warning{Code: "auto_notify_failed", Message: w})
				}
			}
			warnings = append(warnings, syncIfEnabled(dir, taskRef(id))...)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "new owner (required)")
	cmd.MarkFlagRequired("owner")
	return cmd
}
