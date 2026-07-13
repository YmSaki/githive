package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/doctorapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// environmentUnhealthyError carries the diagnostic checks through
// classifyError so --json output can attach them to the error envelope's
// data field, and non-json output can exit with code 5
// (docs/10-cli-spec.md「終了コード」5: 環境不備). It mirrors verify.go's
// verifyFailedError pattern.
type environmentUnhealthyError struct {
	checks []doctorapp.Check
}

func (e *environmentUnhealthyError) Error() string {
	return "githive doctor: environment has errors"
}

// asEnvironmentUnhealthyError is a small errors.As helper so root.go's
// classifyError doesn't need to import doctorapp's error shape directly.
func asEnvironmentUnhealthyError(err error) (*environmentUnhealthyError, bool) {
	var eue *environmentUnhealthyError
	if errors.As(err, &eue) {
		return eue, true
	}
	return nil, false
}

// newDoctorCmd implements `githive doctor`: a read-only environment
// diagnosis (docs/10-cli-spec.md「コマンド体系」). It is distinct from
// `githive status` (project state); doctor checks whether the machine is
// set up correctly to use githive.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the environment: git version, refspec, clock, identity, signing",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			ctx := context.Background()
			report, err := doctorapp.New(dir).Diagnose(ctx, flags.remote)
			if err != nil {
				return err
			}

			if !flags.json {
				for _, c := range report.Checks {
					fmt.Printf("%-8s %-16s %s\n", strings.ToUpper(c.Severity), c.Name, c.Detail)
				}
			}
			if report.HasError() {
				return &environmentUnhealthyError{checks: report.Checks}
			}
			if flags.json {
				cliout.PrintSuccess(map[string]any{"checks": checksToAny(report.Checks)}, nil)
			}
			return nil
		},
	}
}

// checksToAny renders diagnostic checks as the stable --json data shape
// ([{name, severity, detail}, ...]). Shared with root.go's classifyError so
// the error envelope on exit 5 carries the same shape as the success one.
func checksToAny(checks []doctorapp.Check) []any {
	out := make([]any, len(checks))
	for i, c := range checks {
		out[i] = map[string]any{
			"name":     c.Name,
			"severity": c.Severity,
			"detail":   c.Detail,
		}
	}
	return out
}
