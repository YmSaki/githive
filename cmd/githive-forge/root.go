// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/receiveapp"
	"github.com/ymsaki/githive/internal/forge/hook"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "githive-forge",
		Short:         "githive-forge: a git server that enforces githive's rules",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newHookCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			version := "(devel)"
			if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
				version = info.Main.Version
			}
			cmd.Println("githive-forge " + version)
			return nil
		},
	}
}

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Git server hooks (install as the repository's hook scripts)",
	}
	cmd.AddCommand(newHookPreReceiveCmd())
	return cmd
}

func newHookPreReceiveCmd() *cobra.Command {
	var mode string
	var allowed []string
	cmd := &cobra.Command{
		Use:   "pre-receive",
		Short: "Check pushed ref updates read from stdin (githooks(5))",
		Long: "Refuses pushes to refs/githive-remote/**, non-fast-forward updates of refs/projects/**,\n" +
			"and, with --namespace-mode=strict, refs outside refs/heads, refs/tags, refs/projects\n" +
			"and --allow-namespace prefixes. Run it from the repository's hooks/pre-receive script:\n\n" +
			"  #!/bin/sh\n  exec githive-forge hook pre-receive\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// git はフックをリポジトリ（bare なら GIT_DIR）を cwd として起動する。
			cfg := receiveapp.Config{Mode: receiveapp.NamespaceMode(mode), AllowedNamespaces: allowed}
			if code := hook.RunPreReceive(cmd.Context(), ".", cfg, os.Stdin, os.Stderr); code != hook.ExitAccept {
				return exitCode(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "namespace-mode", string(receiveapp.NamespaceOpen), "open (accept any ref) or strict (refs/heads, refs/tags, refs/projects and --allow-namespace only)")
	cmd.Flags().StringArrayVar(&allowed, "allow-namespace", nil, "extra ref prefix accepted in strict mode (repeatable, e.g. refs/notes/)")
	return cmd
}
