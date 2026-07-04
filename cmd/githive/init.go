package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/cliout"
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
			if flags.json {
				cliout.PrintSuccess(map[string]any{
					"refspec_added":       result.RefspecAdded,
					"log_all_ref_updates": result.LogAllRefUpdates,
					"meta_config_made":    result.MetaConfigMade,
				}, nil)
				return nil
			}
			if result.MetaConfigMade {
				fmt.Println("initialized githive project:", project)
			} else {
				fmt.Println("githive project already initialized")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name recorded in meta/config (default: repo directory name)")
	return cmd
}
