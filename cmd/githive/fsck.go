package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// fsckFailedError carries the fsck report through classifyError so --json
// output can attach findings to the error envelope's data field, and non-json
// output can exit with code 4 (docs/10-cli-spec.md「終了コード」4: 検証失敗,
// スキーマ違反の検出). It mirrors verify.go's verifyFailedError pattern.
//
// Checkpoints that --compact created are still reported even on this error
// path: a schema finding elsewhere does not undo a checkpoint already written.
type fsckFailedError struct {
	report *fsckapp.Report
}

func (e *fsckFailedError) Error() string {
	return fmt.Sprintf("githive fsck: %d schema/portability problem(s) found", len(e.report.Findings))
}

// asFsckFailedError is a small errors.As helper so root.go's classifyError
// doesn't need to import fsckapp's error shape directly. root.go wires it to
// cliout.ExitVerifyFailed (exit 4), like asVerifyFailedError.
func asFsckFailedError(err error) (*fsckFailedError, bool) {
	var ffe *fsckFailedError
	if errors.As(err, &ffe) {
		return ffe, true
	}
	return nil, false
}

// newFsckCmd implements `githive fsck [--compact]`
// (docs/10-cli-spec.md「コマンド体系」). Without --compact it is a read-only
// integrity pass; with --compact it also writes checkpoints to entity chains
// past the docs/03 threshold.
func newFsckCmd() *cobra.Command {
	var compact bool
	cmd := &cobra.Command{
		Use:   "fsck",
		Short: "Validate event schemas and (with --compact) create checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			report, err := fsckapp.New(dir).Run(context.Background(), fsckapp.Options{Compact: compact})
			if err != nil {
				return err
			}

			if !flags.json {
				for _, cp := range report.Checkpoints {
					fmt.Printf("checkpoint %s: %s (%d events, %s)\n", cp.Feature, shortID(cp.EventID), cp.EventCount, cp.Reason)
				}
				for _, f := range report.Findings {
					where := f.Commit
					if where == "" {
						where = f.Path
					}
					fmt.Printf("%s: %s %s: %s\n", f.Ref, shortID(where), f.Code, f.Message)
				}
			}
			if !report.OK() {
				return &fsckFailedError{report: report}
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{
					"findings":    findingsToAny(report.Findings),
					"checkpoints": checkpointsToAny(report.Checkpoints),
				}, nil)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&compact, "compact", false, "append a checkpoint to entity chains past the 500-event / 90-day threshold")
	return cmd
}

func findingsToAny(findings []fsckapp.Finding) []any {
	out := make([]any, len(findings))
	for i, f := range findings {
		out[i] = map[string]any{
			"ref":     f.Ref,
			"feature": f.Feature,
			"commit":  f.Commit,
			"path":    f.Path,
			"code":    f.Code,
			"message": f.Message,
		}
	}
	return out
}

func checkpointsToAny(cps []fsckapp.CheckpointInfo) []any {
	out := make([]any, len(cps))
	for i, cp := range cps {
		out[i] = map[string]any{
			"ref":         cp.Ref,
			"feature":     cp.Feature,
			"entity":      cp.Entity,
			"event_id":    cp.EventID,
			"event_count": cp.EventCount,
			"reason":      cp.Reason,
		}
	}
	return out
}
