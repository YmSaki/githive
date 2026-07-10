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

// ItemsMap builds the { "items": [...], "total": total } shape that list
// commands return (docs/10-cli-spec.md「一覧系は { "items": [...], "total":
// n } で統一する」). It does not print; callers that need pagination fields
// alongside items/total (e.g. an MCP tool adding "next_cursor") add those to
// the returned map before returning it themselves.
//
// T is constrained to map[string]any-shaped types (rather than any) because
// internal/core/event's canonical JSON encoder only renders
// map[string]any/[]any/primitives (internal/core/event/canonical.go
// encodeValue); passing anything else would build fine here but fail at
// print time with an opaque "unsupported type" error. The constraint turns
// that mismatch into a compile error instead.
func ItemsMap[T ~map[string]any](items []T, total int) map[string]any {
	anyItems := make([]any, len(items))
	for i, item := range items {
		anyItems[i] = item
	}
	return map[string]any{"items": anyItems, "total": total}
}

// PrintList writes the {"ok":true,"data":{"items":[...],"total":n}}
// envelope for a non-paginated list command, where total is always
// len(items). Paginated results (where total can exceed len(items)) build
// their own map with ItemsMap instead of using this helper.
func PrintList[T ~map[string]any](items []T, warnings []Warning) {
	PrintSuccess(ItemsMap(items, len(items)), warnings)
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
