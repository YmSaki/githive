package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func newSyncCmd() *cobra.Command {
	var kinds []string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "fetch -> merge -> push every githive ref (or a --kind subset)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			refs, err := refsToSync(context.Background(), dir, kinds)
			if err != nil {
				return err
			}
			results, err := syncapp.Sync(context.Background(), dir, flags.remote, refs, syncapp.DefaultRetries)
			if err != nil {
				return err
			}
			if flags.json {
				items := make([]any, len(results))
				for i, r := range results {
					items[i] = map[string]any{"ref": r.Ref, "action": string(r.Action)}
				}
				cliout.PrintSuccess(map[string]any{"items": items, "total": len(items)}, nil)
				return nil
			}
			for _, r := range results {
				fmt.Printf("%s\t%s\n", r.Action, r.Ref)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "comma-separated feature list to sync (default: all supported)")
	return cmd
}

// refsToSync enumerates every ref under refs/projects/ restricted to kinds
// (issue/task/chat are supported by sync; notify/users/wiki are not yet).
func refsToSync(ctx context.Context, dir string, kinds []string) ([]string, error) {
	r := gitx.New(dir)
	entries, err := r.ForEachRef(ctx, "refs/projects/")
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, k := range kinds {
		allowed[k] = true
	}
	var refs []string
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err != nil {
			continue
		}
		switch parsed.Feature {
		case refspace.FeatureIssue, refspace.FeatureTask, refspace.FeatureChat:
			// supported by syncapp; notify/users/wiki are not yet wired in.
		default:
			continue
		}
		if len(allowed) > 0 && !allowed[string(parsed.Feature)] {
			continue
		}
		refs = append(refs, e.Ref)
	}
	return refs, nil
}
