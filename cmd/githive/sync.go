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
			refs, err := refsToSync(context.Background(), dir, flags.remote, kinds)
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

// refsToSync enumerates every ref to sync, restricted to kinds, among the
// features syncapp.SupportedFeatures actually knows how to merge.
//
// It is not enough to look at local refs/projects/*: an entity another
// clone created (an issue/task/chat/notify post this clone has never seen)
// only exists in the remote's refs at this point, and if we only ever
// looked at what's already local, `githive sync` could never discover or
// fast-forward in anything created elsewhere - it would just silently
// re-confirm "up-to-date" for zero refs and do nothing
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」implies syncing
// "対象 ref" as the full working set, not just already-known ones). So this
// fetches the whole namespace first, then unions local refs with whatever
// showed up in the remote-tracking namespace (refs/githive-remote/*).
func refsToSync(ctx context.Context, dir, remote string, kinds []string) ([]string, error) {
	r := gitx.New(dir)
	if err := r.Fetch(ctx, remote, fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)); err != nil {
		return nil, err
	}

	localEntries, err := r.ForEachRef(ctx, "refs/projects/")
	if err != nil {
		return nil, err
	}
	trackingEntries, err := r.ForEachRef(ctx, "refs/githive-remote/")
	if err != nil {
		return nil, err
	}

	allowed := map[string]bool{}
	for _, k := range kinds {
		allowed[k] = true
	}

	seen := map[string]bool{}
	var refs []string
	add := func(ref string) {
		parsed, err := refspace.Parse(ref)
		if err != nil || !syncapp.SupportedFeatures[parsed.Feature] {
			return
		}
		if len(allowed) > 0 && !allowed[string(parsed.Feature)] {
			return
		}
		if seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}

	for _, e := range localEntries {
		add(e.Ref)
	}
	for _, e := range trackingEntries {
		localRef, err := refspace.LocalRefFromTracking(e.Ref)
		if err != nil {
			continue
		}
		add(localRef)
	}
	return refs, nil
}
