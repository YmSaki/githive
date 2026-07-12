package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/wikiapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// newWikiCmd implements the read side of `githive wiki` (show/log)
// (docs/10-cli-spec.md「コマンド体系」, docs/features/wiki.md). The write side
// (edit/save) is a separate change. wiki is the one feature that does not use
// event sourcing: show/log read the plain git branch refs/projects/wiki/main.
func newWikiCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "wiki", Short: "Browse the wiki (show/log)"}
	cmd.AddCommand(newWikiShowCmd(), newWikiLogCmd())
	return cmd
}

func newWikiShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <path>",
		Short: "Show a wiki page (git show refs/projects/wiki/main:<path>)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			content, err := wikiapp.New(dir).Show(context.Background(), args[0])
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"path": args[0], "content": string(content)}, nil)
				return nil
			}
			if _, err := os.Stdout.Write(content); err != nil {
				return err
			}
			return nil
		},
	}
}

func newWikiLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log [path]",
		Short: "Show the wiki commit history (git log refs/projects/wiki/main)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			var path string
			if len(args) == 1 {
				path = args[0]
			}
			entries, err := wikiapp.New(dir).Log(context.Background(), path)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintList(entries, nil)
				return nil
			}
			for _, e := range entries {
				fmt.Printf("%s  %s  %-24v %v\n", shortHash(e["hash"]), e["date"], e["author"], e["subject"])
			}
			return nil
		},
	}
}

// shortHash abbreviates a commit hash to its first 8 characters for the
// human-readable log; the full hash is preserved in --json output.
func shortHash(v any) string {
	s, _ := v.(string)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
