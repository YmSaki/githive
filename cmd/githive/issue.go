package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "issue", Short: "Manage issues"}
	cmd.AddCommand(
		newIssueNewCmd(),
		newIssueListCmd(),
		newIssueShowCmd(),
		newIssueCommentCmd(),
		newIssueStatusCmd(),
		newIssueEditCmd(),
		newIssueLabelCmd(),
		newIssueAssignCmd(),
		newIssueLinkCmd(),
	)
	return cmd
}

func issueRef(id string) string { return "refs/projects/issue/" + id }

func resolveIssueID(ctx context.Context, svc *issueapp.Service, arg string) (string, error) {
	return svc.ResolveID(ctx, arg)
}

func newIssueNewCmd() *cobra.Command {
	var title, body, bodyFile string
	var labels, assignees []string
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new issue",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			if bodyFile != "" {
				content, err := readFile(bodyFile)
				if err != nil {
					return err
				}
				body = content
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := svc.NewIssue(ctx, title, body, labels, assignees)
			if err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, issueRef(id))
			if flags.json {
				cliout.PrintSuccess(map[string]any{"id": id}, warnings)
				return nil
			}
			fmt.Println(id)
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "issue title (required)")
	cmd.Flags().StringVar(&body, "body", "", "issue body")
	cmd.Flags().StringVarP(&bodyFile, "file", "F", "", "read body from file")
	cmd.Flags().StringSliceVar(&labels, "label", nil, "labels to add")
	cmd.Flags().StringSliceVar(&assignees, "assign", nil, "assignees to add")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newIssueListCmd() *cobra.Command {
	var status, label, assignee string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			items, err := svc.List(context.Background(), issueapp.ListFilter{Status: status, Label: label, Assignee: assignee})
			if err != nil {
				return err
			}
			if flags.json {
				anyItems := make([]any, len(items))
				for i, m := range items {
					anyItems[i] = m
				}
				cliout.PrintSuccess(map[string]any{"items": anyItems, "total": len(items)}, nil)
				return nil
			}
			fmt.Printf("%-10s %-12s %-40s %s\n", "ID", "STATUS", "TITLE", "ASSIGNEE")
			for _, m := range items {
				fmt.Printf("%-10s %-12v %-40v %v\n", shortID(m["id"]), m["status"], m["title"], m["assignees"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&label, "label", "", "filter by label")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	return cmd
}

func newIssueShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return err
			}
			if flags.json {
				comments := make([]any, len(show.Comments))
				for i, c := range show.Comments {
					comments[i] = c
				}
				cliout.PrintSuccess(map[string]any{"meta": show.Meta, "body": show.Body, "comments": comments}, nil)
				return nil
			}
			fmt.Printf("%v  %v  %v\n", show.Meta["id"], show.Meta["status"], show.Meta["title"])
			if show.Body != "" {
				fmt.Println()
				fmt.Println(show.Body)
			}
			for _, c := range show.Comments {
				fmt.Printf("\n--- %v (%v) ---\n%v\n", c["author"], c["ts"], c["body"])
			}
			return nil
		},
	}
	return cmd
}

func newIssueCommentCmd() *cobra.Command {
	var message, file, replyTo, supersedes string
	cmd := &cobra.Command{
		Use:   "comment <id>",
		Short: "Add a comment",
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
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Comment(ctx, id, message, replyTo, supersedes); err != nil {
				return err
			}
			var warnStrings []string
			if replyTo != "" {
				// Notify the author being replied to, unless they are
				// replying to themselves (docs/features/notify.md「自動通知」).
				if show, showErr := svc.Show(ctx, id); showErr == nil {
					selfEmail := ""
					if sig, sigErr := identity.Resolve(ctx, dir); sigErr == nil {
						selfEmail = sig.Email
					}
					for _, c := range show.Comments {
						if c["id"] == replyTo {
							if author, ok := c["author"].(string); ok && author != "" && author != selfEmail {
								warnStrings = append(warnStrings, autoNotify(ctx, dir, "user:"+author,
									fmt.Sprintf("issue %s にコメントが返信されました", shortID(id)),
									map[string]any{"kind": "issue", "id": id})...)
							}
							break
						}
					}
				}
			}
			warnings := syncIfEnabled(dir, issueRef(id))
			for _, w := range warnStrings {
				warnings = append(warnings, cliout.Warning{Code: "auto_notify_failed", Message: w})
			}
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "comment body")
	cmd.Flags().StringVarP(&file, "file", "F", "", "read comment body from file")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "event id this comment replies to")
	cmd.Flags().StringVar(&supersedes, "supersedes", "", "event id of the comment this replaces")
	return cmd
}

func newIssueStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <id> <to>",
		Short: "Transition an issue's status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			ok, err := svc.Status(ctx, id, args[1])
			if err != nil {
				return err
			}
			var warnings []cliout.Warning
			if !ok {
				warnings = append(warnings, cliout.Warning{Code: "invalid_transition", Message: fmt.Sprintf("transition to %q was rejected", args[1])})
			}
			warnings = append(warnings, syncIfEnabled(dir, issueRef(id))...)
			if flags.json {
				cliout.PrintSuccess(map[string]any{"applied": ok}, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	return cmd
}

func newIssueEditCmd() *cobra.Command {
	var title, body string
	var hasTitle, hasBody bool
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit an issue's title/body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			var titlePtr, bodyPtr *string
			if hasTitle {
				titlePtr = &title
			}
			if hasBody {
				bodyPtr = &body
			}
			if err := svc.Edit(ctx, id, titlePtr, bodyPtr); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, issueRef(id))
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&body, "body", "", "new body")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		hasTitle = cmd.Flags().Changed("title")
		hasBody = cmd.Flags().Changed("body")
		return nil
	}
	return cmd
}

func newIssueLabelCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "label <id>",
		Short: "Add/remove labels",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetOp(args[0], add, remove, (*issueapp.Service).Label)
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "labels to add")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "labels to remove")
	return cmd
}

func newIssueAssignCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "assign <id>",
		Short: "Add/remove assignees",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Assign(ctx, id, add, remove); err != nil {
				return err
			}
			// Notify newly-added assignees (docs/features/notify.md「自動通知」).
			var warnings []cliout.Warning
			for _, assignee := range add {
				for _, w := range autoNotify(ctx, dir, "user:"+assignee,
					fmt.Sprintf("issue %s の担当になりました", shortID(id)),
					map[string]any{"kind": "issue", "id": id}) {
					warnings = append(warnings, cliout.Warning{Code: "auto_notify_failed", Message: w})
				}
			}
			warnings = append(warnings, syncIfEnabled(dir, issueRef(id))...)
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "assignees to add")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "assignees to remove")
	return cmd
}

// runSetOp shares the add/remove-set command pattern between label and
// assign (both call a *issueapp.Service method with the same signature).
func runSetOp(idArg string, add, remove []string, op func(*issueapp.Service, context.Context, string, []string, []string) error) error {
	dir, err := repoDir()
	if err != nil {
		return err
	}
	svc := issueapp.New(dir)
	ctx := context.Background()
	id, err := resolveIssueID(ctx, svc, idArg)
	if err != nil {
		return err
	}
	if err := op(svc, ctx, id, add, remove); err != nil {
		return err
	}
	warnings := syncIfEnabled(dir, issueRef(id))
	if flags.json {
		cliout.PrintSuccess(nil, warnings)
		return nil
	}
	printWarnings(warnings)
	return nil
}

func newIssueLinkCmd() *cobra.Command {
	var rel string
	var remove bool
	cmd := &cobra.Command{
		Use:   "link <id> <linked-id>",
		Short: "Link this issue to another entity",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := issueapp.New(dir)
			ctx := context.Background()
			id, err := resolveIssueID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Link(ctx, id, rel, args[1], remove); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, issueRef(id))
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&rel, "rel", "task", "link relation: task/issue/chat")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the link instead of adding it")
	return cmd
}

func printWarnings(warnings []cliout.Warning) {
	for _, w := range warnings {
		fmt.Printf("warning: %s: %s\n", w.Code, w.Message)
	}
}

func shortID(v any) string {
	s, _ := v.(string)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}
