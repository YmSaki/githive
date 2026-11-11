package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/apperr"
	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/logapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/app/wikiapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/sign"
)

// globalFlags holds the persistent flags every subcommand shares
// (docs/10-cli-spec.md「グローバルフラグ」).
type globalFlags struct {
	repo   string
	json   bool
	noSync bool
	remote string
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
	root.AddCommand(newLogCmd())
	root.AddCommand(newWikiCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newFsckCmd())
	root.AddCommand(newMcpCmd())
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
	var replaced *localWriteReplacedError
	if errors.As(err, &replaced) {
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "local_write_replaced", Message: err.Error(), Retryable: false, Data: replaced.data()}
	}
	var syncFailure *syncFailedError
	if errors.As(err, &syncFailure) {
		code, info := classifyError(syncFailure.cause)
		if info.Data == nil {
			info.Data = map[string]any{}
		}
		for key, value := range syncResultData(syncFailure.results) {
			info.Data[key] = value
		}
		return code, info
	}
	var shallow *chain.ShallowHistoryError
	if errors.As(err, &shallow) {
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "shallow_history", Message: err.Error()}
	}
	if errors.Is(err, syncapp.ErrMetaConfigConflict) {
		data := map[string]any{"ref": refspace.MetaConfigRef}
		var conflict *syncapp.MetaConfigConflictError
		if errors.As(err, &conflict) {
			data["local_oid"] = conflict.LocalOID
			data["remote_oid"] = conflict.RemoteOID
			data["recovery"] = conflict.Recovery()
		}
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "meta_config_conflict", Message: err.Error(), Data: data}
	}

	var clockSkew *entitychain.ClockSkewError
	if errors.As(err, &clockSkew) {
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "clock_skew", Message: err.Error(), Data: map[string]any{
			"event_id": clockSkew.EventID, "latest_event_id": clockSkew.LatestID,
			"latest_timestamp": clockSkew.LatestTimestamp, "recovery": clockSkew.Recovery(),
		}}
	}
	var invalidInput *event.InvalidInputError
	if errors.As(err, &invalidInput) {
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_input", Message: err.Error()}
	}
	var invalidData *event.DataSchemaError
	if errors.As(err, &invalidData) {
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "invalid_event_data", Message: err.Error()}
	}
	var ambiguous *apperr.AmbiguousIDError
	var schemaUnsupported *entitychain.SchemaUnsupportedError
	var invalidConfig *entitychain.InvalidConfigError
	if errors.As(err, &invalidConfig) {
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "invalid_config", Message: err.Error()}
	}
	if errors.As(err, &schemaUnsupported) {
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "schema_unsupported", Message: err.Error()}
	}
	if vfe, ok := asVerifyFailedError(err); ok {
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{
			Code:    "verify_failed",
			Message: err.Error(),
			Data:    map[string]any{"reports": reportsToAny(vfe.reports)},
		}
	}
	if eue, ok := asEnvironmentUnhealthyError(err); ok {
		return cliout.ExitEnvironment, cliout.ErrorInfo{
			Code:    "environment_unhealthy",
			Message: err.Error(),
			Data:    map[string]any{"checks": checksToAny(eue.checks)},
		}
	}
	if ffe, ok := asFsckFailedError(err); ok {
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{
			Code:    "fsck_failed",
			Message: err.Error(),
			Data:    map[string]any{"findings": findingsToAny(ffe.report.Findings), "checkpoints": checkpointsToAny(ffe.report.Checkpoints)},
		}
	}
	var trustRootConflict *syncapp.TrustRootConflictError
	var wikiValidation *wikiapp.ValidationError
	var wikiConflict *wikiapp.ConflictError
	switch {
	case errors.Is(err, event.ErrEventTooLarge):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "event_too_large", Message: err.Error()}
	case errors.Is(err, refspace.ErrEventRefMismatch):
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "event_ref_mismatch", Message: err.Error()}
	case errors.Is(err, event.ErrEventOrderViolation):
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "event_order_violation", Message: err.Error()}
	case errors.Is(err, event.ErrConflictingEventID):
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "conflicting_event_id", Message: err.Error()}
	case errors.Is(err, errVerifyAllWithRef):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "usage_error", Message: err.Error()}
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
	case errors.Is(err, sign.ErrUnsupportedFormat), errors.Is(err, sign.ErrSigningKeyMissing):
		// Signing was asked for but cannot be performed: environment
		// misconfiguration, not a usage error (ADR-0014, `githive doctor`'s
		// signing check reports the same condition).
		return cliout.ExitEnvironment, cliout.ErrorInfo{Code: "signing_misconfigured", Message: err.Error()}
	case errors.As(err, &trustRootConflict):
		// Two competing users registries: a verification failure that needs a
		// human decision, not a retryable sync race (docs/03「共通祖先が無い
		// 場合」, docs/10「終了コード」4).
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{
			Code:    "trust_root_conflict",
			Message: err.Error(),
			Data: map[string]any{
				"ref":          trustRootConflict.Ref,
				"local_roots":  toAnySlice(trustRootConflict.LocalRoots),
				"remote_roots": toAnySlice(trustRootConflict.RemoteRoots),
				"pin":          trustRootConflict.Pin,
				"recovery":     trustRootConflict.Recovery(),
			},
		}
	case errors.Is(err, usersapp.ErrInvalidName):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_name", Message: err.Error()}
	case errors.Is(err, usersapp.ErrInvalidPolicy):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_policy", Message: err.Error()}
	case errors.Is(err, gitx.ErrInvalidRemote), errors.Is(err, errMcpRemoteNotAllowed):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_remote", Message: err.Error()}
	case errors.Is(err, gitx.ErrInvalidRef):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_ref", Message: err.Error()}
	case errors.Is(err, usersapp.ErrPrivateKey):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "private_key_rejected", Message: err.Error()}
	case errors.Is(err, usersapp.ErrInvalidKey):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_key", Message: err.Error()}
	case errors.Is(err, usersapp.ErrInvalidEmail):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_email", Message: err.Error()}
	case errors.Is(err, usersapp.ErrEmailExists):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "email_exists", Message: err.Error()}
	case errors.Is(err, usersapp.ErrKeyExists):
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "already_exists", Message: err.Error()}
	case errors.Is(err, errInvalidNotifyPriority):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_priority", Message: err.Error()}
	case errors.Is(err, logapp.ErrInvalidSince):
		return cliout.ExitUsageError, cliout.ErrorInfo{Code: "invalid_since", Message: err.Error()}
	case errors.Is(err, idresolve.ErrNotFound), errors.Is(err, taskapp.ErrNotFound), errors.Is(err, chatapp.ErrNotFound), errors.Is(err, notifyapp.ErrNotFound), errors.Is(err, issueapp.ErrNotFound), errors.Is(err, usersapp.ErrUserNotFound), errors.Is(err, usersapp.ErrGroupNotFound), errors.Is(err, usersapp.ErrKeyNotFound), errors.Is(err, wikiapp.ErrNotFound):
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "not_found", Message: err.Error()}
	case errors.Is(err, issueapp.ErrRetriesExhausted), errors.Is(err, syncapp.ErrRetriesExhausted):
		return cliout.ExitSyncRetryExhausted, cliout.ErrorInfo{Code: "conflict_retry_exhausted", Message: err.Error(), Retryable: true}
	case errors.Is(err, syncapp.ErrPushRejected), errors.Is(err, wikiapp.ErrPushRejected):
		// A permanent push rejection (pre-receive hook, permission error,
		// ...), not a non-fast-forward race - retrying will not help, so
		// this is exit 1 (general error) rather than exit 3
		// (docs/10-cli-spec.md「終了コード」).
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "push_rejected", Message: err.Error(), Retryable: false}
	case errors.As(err, &wikiValidation):
		items := make([]any, len(wikiValidation.Violations))
		for i, v := range wikiValidation.Violations {
			items[i] = map[string]any{"path": v.Path, "code": v.Code, "message": v.Message}
		}
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "wiki_validation_failed", Message: err.Error(), Data: map[string]any{"violations": items}}
	case errors.As(err, &wikiConflict):
		return cliout.ExitSyncRetryExhausted, cliout.ErrorInfo{Code: "wiki_conflict", Message: err.Error(), Retryable: false, Data: map[string]any{"paths": toAnySlice(wikiConflict.Paths)}}
	case errors.Is(err, wikiapp.ErrPushRaceExhausted):
		return cliout.ExitSyncRetryExhausted, cliout.ErrorInfo{Code: "wiki_push_race", Message: err.Error(), Retryable: true}
	case errors.Is(err, wikiapp.ErrWorktreeExists):
		// A prior `wiki edit` is still outstanding (issue #57): the fix is to
		// `wiki save` or explicitly discard it, not to retry, so
		// Retryable defaults to false.
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "wiki_worktree_exists", Message: err.Error()}
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

// syncIfEnabled separates durable local writes from publication. Network
// failures are warnings while the local write survives; replacing that write
// during trust-root resolution is an explicit, non-retryable failure.
func syncIfEnabled(dir, ref string, prior ...[]cliout.Warning) ([]cliout.Warning, error) {
	var warnings []cliout.Warning
	for _, ws := range prior {
		warnings = append(warnings, ws...)
	}
	if flags.noSync {
		return warnings, nil
	}
	ctx := context.Background()
	retries := syncapp.RetriesFromConfig(ctx, dir)
	refs := []string{ref}
	if ref != refspace.NotifyStreamRef {
		refs = append(refs, refspace.NotifyStreamRef)
	}
	results, syncErr := syncapp.Sync(ctx, dir, flags.remote, refs, retries)
	_, resultWarnings := syncResultItems(results)
	warnings = append(warnings, resultWarnings...)
	published := false
	for _, result := range results {
		if result.Ref == ref {
			switch result.Action {
			case syncapp.ActionUpToDate, syncapp.ActionFastForwardIn, syncapp.ActionFastForwardOut, syncapp.ActionMerged:
				published = true
			}
			if result.Action == syncapp.ActionAdoptedRemoteTrustRoot {
				return nil, &localWriteReplacedError{result: result, results: results, warnings: warnings, cause: syncErr}
			}
		}
	}
	if syncErr != nil {
		data := syncResultData(results)
		data["local_written"] = true
		data["local_write_preserved"] = true
		data["write_ref"] = ref
		data["published"] = published
		data["delivery_complete"] = false
		warnings = append(warnings, cliout.Warning{Code: "sync_failed", Message: syncErr.Error(), Data: data})
	}
	return warnings, nil
}

// localWriteReplacedError retains the local commit receipt even after the
// authoritative ref adopts an unrelated remote root. Never automatically retry.
type localWriteReplacedError struct {
	result   syncapp.Result
	results  []syncapp.Result
	warnings []cliout.Warning
	cause    error
}

func (e *localWriteReplacedError) Error() string {
	return fmt.Sprintf("local write at %s was replaced by remote %s during sync of %s. %s", e.result.LocalOID, e.result.RemoteOID, e.result.Ref, e.result.Recovery)
}
func (e *localWriteReplacedError) Unwrap() error { return e.cause }
func (e *localWriteReplacedError) data() map[string]any {
	data := syncResultData(e.results)
	data["local_written"] = true
	data["local_write_preserved"] = false
	data["published"] = false
	data["ref"] = e.result.Ref
	data["local_oid"] = e.result.LocalOID
	data["remote_oid"] = e.result.RemoteOID
	data["backup_ref"] = e.result.BackupRef
	data["recovery"] = e.result.Recovery
	if e.cause != nil {
		data["sync_error"] = e.cause.Error()
	}
	if len(e.warnings) > 0 {
		data["warnings"] = warningItems(e.warnings)
	}
	return data
}
func warningItems(warnings []cliout.Warning) []any {
	out := make([]any, len(warnings))
	for i, w := range warnings {
		item := map[string]any{"code": w.Code, "message": w.Message}
		if w.Data != nil {
			item["data"] = w.Data
		}
		out[i] = item
	}
	return out
}
