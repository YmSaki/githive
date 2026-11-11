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
			items, warnings := syncResultItems(results)
			if flags.json {
				if err != nil {
					return &syncFailedError{cause: err, results: results}
				}
				return cliout.PrintList(items, warnings)
			}
			for _, r := range results {
				fmt.Printf("%s\t%s\n", r.Action, r.Ref)
			}
			printWarnings(warnings)
			return err
		},
	}
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "comma-separated feature list to sync (default: all supported)")
	return cmd
}

// syncFailedError preserves completed refs through the CLI error path without
// changing the underlying error's exit code or diagnostic fields.
type syncFailedError struct {
	cause   error
	results []syncapp.Result
}

func (e *syncFailedError) Error() string { return e.cause.Error() }
func (e *syncFailedError) Unwrap() error { return e.cause }

func syncResultItems(results []syncapp.Result) ([]map[string]any, []cliout.Warning) {
	items := make([]map[string]any, len(results))
	var warnings []cliout.Warning
	for i, r := range results {
		items[i] = map[string]any{"ref": r.Ref, "action": string(r.Action)}
		if r.LocalOID != "" {
			items[i]["local_oid"] = r.LocalOID
		}
		if r.RemoteOID != "" {
			items[i]["remote_oid"] = r.RemoteOID
		}
		if r.BackupRef != "" {
			items[i]["backup_ref"] = r.BackupRef
		}
		if r.Recovery != "" {
			items[i]["recovery"] = r.Recovery
		}
		if r.Action == syncapp.ActionAdoptedRemoteTrustRoot || r.Action == syncapp.ActionKeptLocalTrustRoot {
			items[i]["published"] = false
			items[i]["local_write_preserved"] = r.Action != syncapp.ActionAdoptedRemoteTrustRoot
		}
		if r.Warning != "" {
			items[i]["warning"] = r.Warning
			warnings = append(warnings, cliout.Warning{Code: "sync_warning", Message: r.Warning, Data: resolutionData(r)})
		}
	}
	return items, warnings
}

func syncResultData(results []syncapp.Result) map[string]any {
	items, warnings := syncResultItems(results)
	data := cliout.ItemsMap(items, len(items))
	if len(warnings) > 0 {
		data["warnings"] = warningItems(warnings)
	}
	return data
}

func resolutionData(r syncapp.Result) map[string]any {
	if r.LocalOID == "" && r.RemoteOID == "" {
		return nil
	}
	return map[string]any{"ref": r.Ref, "local_oid": r.LocalOID, "remote_oid": r.RemoteOID, "backup_ref": r.BackupRef, "recovery": r.Recovery, "published": false, "local_write_preserved": r.Action != syncapp.ActionAdoptedRemoteTrustRoot}
}
