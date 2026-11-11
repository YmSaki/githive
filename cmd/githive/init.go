package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func newInitCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure the remote tracking refspec and create meta/config",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			if project == "" {
				project = filepath.Base(dir)
			}
			result, err := initapp.Init(context.Background(), dir, flags.remote, project)
			if err != nil {
				return err
			}
			var warnings []cliout.Warning
			for _, w := range result.Warnings {
				warnings = append(warnings, cliout.Warning{Code: "init_warning", Message: w})
			}
			if result.MetaConfigMade && result.Remote.Configured {
				syncedWarnings, syncErr := syncIfEnabled(dir, refspace.MetaConfigRef, warnings)
				if syncErr != nil {
					return syncErr
				}
				warnings = syncedWarnings
			}
			if flags.json {
				adopted := make([]any, len(result.Remote.AdoptedRefs))
				for i, ref := range result.Remote.AdoptedRefs {
					adopted[i] = ref
				}
				return cliout.PrintSuccess(map[string]any{
					"refspec_added":       result.RefspecAdded,
					"log_all_ref_updates": result.LogAllRefUpdates,
					"meta_config_made":    result.MetaConfigMade,
					"remote": map[string]any{
						"name":            result.Remote.Name,
						"configured":      result.Remote.Configured,
						"fetched":         result.Remote.Fetched,
						"has_meta_config": result.Remote.HasMetaConfig,
						"adopted_refs":    adopted,
					},
				}, warnings)
			}
			printWarnings(warnings)
			switch {
			case result.MetaConfigMade:
				fmt.Println("initialized githive project:", project)
			case len(result.Remote.AdoptedRefs) > 0:
				fmt.Printf("joined existing githive project from %s (%d refs fetched)\n", result.Remote.Name, len(result.Remote.AdoptedRefs))
			default:
				fmt.Println("githive project already initialized")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name recorded in meta/config (default: repo directory name)")
	return cmd
}
