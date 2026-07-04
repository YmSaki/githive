// Package cliout renders the CLI's --json output envelope
// (docs/10-cli-spec.md「JSON 出力の封筒」) and defines the exit codes the
// CLI maps errors to (docs/10-cli-spec.md「終了コード」).
//
// It reuses internal/core/event's canonical JSON encoder rather than
// encoding/json directly, keeping exactly one JSON-writing implementation
// in the whole codebase (docs/13-roadmap.md 手戻りリスク表) and getting
// stable, diffable CLI --json output as a side benefit.
package cliout

import (
	"fmt"
	"os"

	"github.com/ymsaki/githive/internal/core/event"
)

// Exit codes (docs/10-cli-spec.md「終了コード」).
const (
	ExitOK                 = 0
	ExitGeneralError       = 1
	ExitUsageError         = 2
	ExitSyncRetryExhausted = 3
	ExitVerifyFailed       = 4
	ExitEnvironment        = 5
)

// Warning is one non-fatal issue attached to an otherwise-successful result
// (e.g. a sync failure after a successful local write).
type Warning struct {
	Code    string
	Message string
}

// ErrorInfo is the shape of a failed command's "error" field.
type ErrorInfo struct {
	Code      string
	Message   string
	Retryable bool
	Data      map[string]any
}

// PrintSuccess writes the {"ok":true,...} envelope to stdout.
func PrintSuccess(data any, warnings []Warning) {
	m := map[string]any{"ok": true}
	if data != nil {
		m["data"] = data
	}
	if len(warnings) > 0 {
		m["warnings"] = warningsToAny(warnings)
	}
	printEnvelope(m)
}

// PrintFailure writes the {"ok":false,"error":...} envelope to stdout.
func PrintFailure(info ErrorInfo) {
	errMap := map[string]any{
		"code":      info.Code,
		"message":   info.Message,
		"retryable": info.Retryable,
	}
	if info.Data != nil {
		errMap["data"] = info.Data
	}
	printEnvelope(map[string]any{"ok": false, "error": errMap})
}

func warningsToAny(warnings []Warning) []any {
	out := make([]any, len(warnings))
	for i, w := range warnings {
		out[i] = map[string]any{"code": w.Code, "message": w.Message}
	}
	return out
}

func printEnvelope(m map[string]any) {
	out, err := event.Encode(m)
	if err != nil {
		// Encoding our own envelope should never fail; if it does, fall
		// back to a minimal hand-written line rather than panicking.
		fmt.Fprintf(os.Stdout, `{"ok":false,"error":{"code":"internal","message":%q,"retryable":false}}`+"\n", err.Error())
		return
	}
	os.Stdout.Write(out)
}
