package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/identity"
)

// globalFlags holds the persistent flags every subcommand shares
// (docs/10-cli-spec.md「グローバルフラグ」).
type globalFlags struct {
	repo    string
	json    bool
	noSync  bool
	remote  string
	quiet   bool
	verbose bool
}

var flags globalFlags

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "githive",
		Short:         "githive: project memory stored in git refs",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flags.repo, "repo", "", "target repository path (default: discover from cwd)")
	root.PersistentFlags().BoolVar(&flags.json, "json", false, "JSON output")
	root.PersistentFlags().BoolVar(&flags.noSync, "no-sync", false, "do not sync after a write")
	root.PersistentFlags().StringVar(&flags.remote, "remote", "origin", "sync remote name")
	root.PersistentFlags().BoolVar(&flags.quiet, "quiet", false, "reduce output")
	root.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "increase output")

	root.AddCommand(newInitCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newIssueCmd())
	root.AddCommand(newTaskCmd())
	root.AddCommand(newChatCmd())
	root.AddCommand(newNotifyCmd())
	root.AddCommand(newUsersCmd())
	root.AddCommand(newVerifyCmd())
	root.AddCommand(newStatusCmd())
	return root
}

func main() {
	os.Exit(run())
}

func run() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		code, info := classifyError(err)
		if flags.json {
			cliout.PrintFailure(info)
		} else {
			fmt.Fprintln(os.Stderr, "error:", info.Message)
		}
		return code
	}
	return cliout.ExitOK
}

// repoDir resolves --repo, or discovers the nearest ancestor directory
// containing .git starting from the current working directory
// (docs/10-cli-spec.md「リポジトリの特定」). It always verifies the
// resolved directory is actually a git repository (exit code 5 otherwise,
// docs/10-cli-spec.md「終了コード」「リポジトリでない」) - git plumbing
// commands like `git config` happily fall back to the user's global config
// even outside any repository, so that alone can't be trusted as a signal.
func repoDir() (string, error) {
	if flags.repo != "" {
		if !isGitRepo(flags.repo) {
			return "", errNotARepo
		}
		return flags.repo, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if isGitRepo(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errNotARepo
		}
		dir = parent
	}
}

// isGitRepo reports whether dir is a normal (has .git) or bare (has HEAD +
// objects/ at its root) git repository.
func isGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "objects"))
	return err == nil
}

var errNotARepo = errors.New("not a githive/git repository (no .git found; use --repo)")

// classifyError maps an error to a CLI exit code and JSON error envelope
// (docs/10-cli-spec.md「終了コード」).
func classifyError(err error) (int, cliout.ErrorInfo) {
	var ambiguous *issueapp.AmbiguousIDError
	if vfe, ok := asVerifyFailedError(err); ok {
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{
			Code:    "verify_failed",
			Message: err.Error(),
			Data:    map[string]any{"reports": reportsToAny(vfe.reports)},
		}
	}
	switch {
	case errors.As(err, &ambiguous):
		return cliout.ExitUsageError, cliout.ErrorInfo{
			Code:    "ambiguous_id",
			Message: err.Error(),
			Data:    map[string]any{"candidates": toAnySlice(ambiguous.Candidates)},
		}
	case errors.Is(err, errNotARepo):
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "not_a_repo", Message: err.Error()}
	case errors.Is(err, identity.ErrNotConfigured), errors.Is(err, issueapp.ErrIdentityNotConfigured):
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "identity_not_configured", Message: err.Error()}
	case errors.Is(err, usersapp.ErrInvalidName):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_name", Message: err.Error()}
	case errors.Is(err, issueapp.ErrNotFound), errors.Is(err, usersapp.ErrUserNotFound), errors.Is(err, usersapp.ErrGroupNotFound):
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "not_found", Message: err.Error()}
	case errors.Is(err, issueapp.ErrRetriesExhausted), errors.Is(err, syncapp.ErrRetriesExhausted):
		return cliout.ExitSyncRetryExhausted, cliout.ErrorInfo{Code: "conflict_retry_exhausted", Message: err.Error(), Retryable: true}
	default:
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "error", Message: err.Error()}
	}
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// syncIfEnabled runs a best-effort sync of ref after a write, unless
// --no-sync was given. A sync failure is reported as a warning, not an
// error - writes complete locally regardless of network outcome
// (docs/01-architecture.md「書き込み」「エラー処理方針」).
func syncIfEnabled(dir, ref string) []cliout.Warning {
	if flags.noSync {
		return nil
	}
	if _, err := syncapp.Sync(context.Background(), dir, flags.remote, []string{ref}, syncapp.DefaultRetries); err != nil {
		return []cliout.Warning{{Code: "sync_failed", Message: err.Error()}}
	}
	return nil
}
