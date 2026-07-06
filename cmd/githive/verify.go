package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// verifyFailedError carries the reports through classifyError so --json
// output can attach them to the error envelope's data field, and
// non-json output can report exit code 4
// (docs/10-cli-spec.md「終了コード」4: 検証失敗).
type verifyFailedError struct {
	reports []verifyapp.RefReport
}

func (e *verifyFailedError) Error() string {
	return "githive verify: one or more issues found"
}

func newVerifyCmd() *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify commit signatures and chain integrity (docs/11-security.md)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			trustRootPin, err := verifyapp.TrustRootPin(ctx, dir)
			if err != nil {
				return err
			}

			var reports []verifyapp.RefReport
			if ref != "" {
				report, err := verifyapp.VerifyRef(ctx, dir, ref, trustRootPin)
				if err != nil {
					return err
				}
				reports = []verifyapp.RefReport{*report}
			} else {
				reports, err = verifyapp.VerifyAll(ctx, dir, trustRootPin)
				if err != nil {
					return err
				}
			}

			ok := true
			for _, r := range reports {
				if !r.OK() {
					ok = false
					break
				}
			}

			if !flags.json {
				for _, r := range reports {
					status := "OK"
					if !r.OK() {
						status = "FAILED"
					}
					fmt.Printf("%s: %s (%d commits)\n", r.Ref, status, r.CommitCount)
					for _, iss := range r.Issues {
						fmt.Printf("  %s %s: %s\n", shortID(iss.CommitHash), iss.Code, iss.Message)
					}
				}
			}
			if !ok {
				return &verifyFailedError{reports: reports}
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"reports": reportsToAny(reports)}, nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "verify only this ref (default: every verifiable ref)")
	return cmd
}

func reportsToAny(reports []verifyapp.RefReport) []any {
	out := make([]any, len(reports))
	for i, r := range reports {
		issues := make([]any, len(r.Issues))
		for j, iss := range r.Issues {
			issues[j] = map[string]any{"commit": iss.CommitHash, "code": iss.Code, "message": iss.Message}
		}
		out[i] = map[string]any{
			"ref":             r.Ref,
			"commit_count":    r.CommitCount,
			"trust_root_hash": r.TrustRootHash,
			"issues":          issues,
		}
	}
	return out
}

// asVerifyFailedError is a small errors.As helper so root.go's
// classifyError doesn't need to import cobra/context.
func asVerifyFailedError(err error) (*verifyFailedError, bool) {
	var vfe *verifyFailedError
	if errors.As(err, &vfe) {
		return vfe, true
	}
	return nil, false
}
