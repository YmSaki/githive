package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/cliout"
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
			ctx := context.Background()
			refs, err := syncapp.DiscoverRefs(ctx, dir, flags.remote, kinds)
			if err != nil {
				return err
			}
			retries := syncapp.RetriesFromConfig(ctx, dir)
			// DiscoverRefs just fetched the namespace; Sync must not repeat it.
			results, err := syncapp.Sync(ctx, dir, flags.remote, refs, retries, syncapp.SkipInitialFetch())
			if err != nil {
				return err
			}
			var warnings []cliout.Warning
			for _, r := range results {
				if r.Warning != "" {
					warnings = append(warnings, cliout.Warning{Code: "sync_warning", Message: r.Warning})
				}
			}
			if flags.json {
				items := make([]map[string]any, len(results))
				for i, r := range results {
					items[i] = map[string]any{"ref": r.Ref, "action": string(r.Action)}
				}
				cliout.PrintList(items, warnings)
				return nil
			}
			for _, r := range results {
				fmt.Printf("%s\t%s\n", r.Action, r.Ref)
			}
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "comma-separated feature list to sync (default: all supported)")
	return cmd
}
