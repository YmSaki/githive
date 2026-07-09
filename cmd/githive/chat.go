package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/cliout"
)

func newChatCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "chat", Short: "Manage chat threads"}
	cmd.AddCommand(
		newChatNewCmd(),
		newChatListCmd(),
		newChatShowCmd(),
		newChatPostCmd(),
		newChatArchiveCmd(),
	)
	return cmd
}

func chatRef(id string) string { return "refs/projects/chat/" + id }

func resolveChatID(ctx context.Context, svc *chatapp.Service, arg string) (string, error) {
	return svc.ResolveID(ctx, arg)
}

func newChatNewCmd() *cobra.Command {
	var title, message string
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new chat thread",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := chatapp.New(dir)
			ctx := context.Background()
			id, err := svc.NewThread(ctx, title, message)
			if err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, chatRef(id))
			if flags.json {
				cliout.PrintSuccess(map[string]any{"id": id}, warnings)
				return nil
			}
			fmt.Println(id)
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "thread title (required)")
	cmd.Flags().StringVarP(&message, "message", "m", "", "first message")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newChatListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chat threads",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := chatapp.New(dir)
			items, err := svc.List(context.Background(), chatapp.ListFilter{Status: status})
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintList(items, nil)
				return nil
			}
			fmt.Printf("%-10s %-10s %-40s %s\n", "ID", "STATUS", "TITLE", "MESSAGES")
			for _, m := range items {
				fmt.Printf("%-10s %-10v %-40v %v\n", shortID(m["id"]), m["status"], m["title"], m["message_count"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (open/archived)")
	return cmd
}

func newChatShowCmd() *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a chat thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := chatapp.New(dir)
			ctx := context.Background()
			id, err := resolveChatID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return err
			}
			messages := show.Messages
			if tail > 0 && len(messages) > tail {
				messages = messages[len(messages)-tail:]
			}
			if flags.json {
				anyMessages := make([]any, len(messages))
				for i, m := range messages {
					anyMessages[i] = m
				}
				cliout.PrintSuccess(map[string]any{"meta": show.Meta, "messages": anyMessages}, nil)
				return nil
			}
			fmt.Printf("%v  %v  %v\n", show.Meta["id"], show.Meta["status"], show.Meta["title"])
			for _, m := range messages {
				fmt.Printf("\n--- %v (%v) ---\n%v\n", m["author"], m["ts"], m["body"])
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 0, "show only the last N messages")
	return cmd
}

// newChatPostCmd does not yet implement docs/features/chat.md「メンション
// と通知」(@username/@group in the body auto-posting a notify.post): that
// requires resolving a username/group to an email, which needs the users
// registry that lands in P3 (docs/13-roadmap.md「P3：users / 署名 /
// verify」). Attempting it now would mean guessing an email from a bare
// "@name" token, producing invalid notify targets
// (spec/schemas/notify.schema.json requires "user:<email>"). This is a
// known, deliberate gap, not an oversight - revisit once the registry
// exists.
func newChatPostCmd() *cobra.Command {
	var message, replyTo string
	cmd := &cobra.Command{
		Use:   "post <id>",
		Short: "Post a message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := chatapp.New(dir)
			ctx := context.Background()
			id, err := resolveChatID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Post(ctx, id, message, replyTo, ""); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, chatRef(id))
			if flags.json {
				cliout.PrintSuccess(nil, warnings)
				return nil
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "message body (required)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "event id this message replies to")
	cmd.MarkFlagRequired("message")
	return cmd
}

func newChatArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a chat thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			svc := chatapp.New(dir)
			ctx := context.Background()
			id, err := resolveChatID(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if err := svc.Archive(ctx, id); err != nil {
				return err
			}
			warnings := syncIfEnabled(dir, chatRef(id))
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
