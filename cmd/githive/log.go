package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/logapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// newLogCmd implements `githive log`: the cross-feature event timeline
// (docs/10-cli-spec.md「コマンド体系」: `githive log [--since] [--actor]`).
func newLogCmd() *cobra.Command {
	var since, actor string
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the cross-feature event timeline",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			entries, err := logapp.New(dir).List(ctx, logapp.ListFilter{Since: since, ActorEmail: actor})
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintList(entries, nil)
				return nil
			}
			for _, e := range entries {
				fmt.Printf("%s  %-8v %-16v %-8s %v\n", e["ts"], e["feature"], e["kind"], shortID(e["entity"]), e["actor"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only events at or after this RFC3339 UTC timestamp")
	cmd.Flags().StringVar(&actor, "actor", "", "only events by this actor (email)")
	return cmd
}
