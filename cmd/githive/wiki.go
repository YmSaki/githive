package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/wikiapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// newWikiCmd implements `githive wiki` (show/log/edit/save)
// (docs/10-cli-spec.md「コマンド体系」, docs/features/wiki.md). wiki is the one
// feature that does not use event sourcing: show/log read, and edit/save write,
// the plain git branch refs/projects/wiki/main via a temporary worktree and
// git's ordinary 3-way merge — never event-union or internal/core/materialize.
func newWikiCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "wiki", Short: "Browse and edit the wiki (show/log/edit/save)"}
	cmd.AddCommand(newWikiShowCmd(), newWikiLogCmd(), newWikiEditCmd(), newWikiSaveCmd())
	return cmd
}

func newWikiEditCmd() *cobra.Command {
	var keep bool
	c := &cobra.Command{
		Use:   "edit",
		Short: "Create a temporary worktree to edit the wiki",
		Long: "Create a temporary git worktree checked out at refs/projects/wiki/main " +
			"(or an empty tree if the wiki does not exist yet) and print its path. " +
			"Edit the files there, then run `githive wiki save`. With --keep the worktree " +
			"is yours to drive with ordinary `git -C <path> ...` commands before saving.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			wt, err := wikiapp.New(dir).Edit(context.Background(), keep)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"worktree": wt}, nil)
				return nil
			}
			fmt.Println(wt)
			return nil
		},
	}
	c.Flags().BoolVar(&keep, "keep", false, "keep the worktree for manual git edits; run `githive wiki save` when done")
	return c
}

func newWikiSaveCmd() *cobra.Command {
	var msg string
	c := &cobra.Command{
		Use:   "save",
		Short: "Commit the wiki worktree, 3-way merge with the remote, and push",
		Long: "Commit the wiki edit worktree, reconcile with the remote wiki using git's " +
			"ordinary 3-way merge, and push (unless --no-sync). On a merge conflict the " +
			"conflicted paths are reported and the worktree keeps the conflict markers; " +
			"resolve them and re-run `githive wiki save` to complete.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			res, err := wikiapp.New(dir).Save(context.Background(), msg, flags.remote, flags.noSync)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"hash": res.CommitHash, "pushed": res.Pushed}, nil)
				return nil
			}
			if res.Pushed {
				fmt.Printf("saved and pushed %s\n", res.CommitHash)
			} else {
				fmt.Printf("saved %s (not pushed)\n", res.CommitHash)
			}
			return nil
		},
	}
	c.Flags().StringVarP(&msg, "message", "m", "", "commit message (required)")
	_ = c.MarkFlagRequired("message")
	return c
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
