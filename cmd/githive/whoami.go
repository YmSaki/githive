package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the identity (git user.email) githive will record as actor",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			sig, err := identity.Resolve(context.Background(), dir)
			if err != nil {
				return err
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"name": sig.Name, "email": sig.Email}, nil)
				return nil
			}
			fmt.Printf("%s <%s>\n", sig.Name, sig.Email)
			return nil
		},
	}
}
