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
	"io"
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

// PrintSuccess writes the {"ok":true,...} envelope to stdout. If the
// envelope cannot be encoded, nothing is written and the error is returned:
// the caller must propagate it so the command exits non-zero and the root
// command prints the failure envelope instead (docs/10-cli-spec.md「終了コード」).
func PrintSuccess(data any, warnings []Warning) error {
	m := map[string]any{"ok": true}
	if data != nil {
		m["data"] = data
	}
	if len(warnings) > 0 {
		m["warnings"] = warningsToAny(warnings)
	}
	return printEnvelope(m)
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
	if err := printEnvelope(map[string]any{"ok": false, "error": errMap}); err != nil {
		// error.data は呼び出し側の任意値なので encode に失敗しうる。data を
		// 落とした最小の封筒でも canonical エンコーダを通す（手書き JSON は書かない）。
		fallback := map[string]any{
			"code":      info.Code,
			"message":   info.Message,
			"retryable": info.Retryable,
		}
		if err := printEnvelope(map[string]any{"ok": false, "error": fallback}); err != nil {
			fmt.Fprintln(os.Stderr, "error: cannot encode failure envelope:", err)
		}
	}
}

// ItemsMap builds the { "items": [...], "total": total } shape that list
// commands return (docs/10-cli-spec.md「一覧系は { "items": [...], "total":
// n } で統一する」). It does not print; callers that need pagination fields
// alongside items/total (e.g. an MCP tool adding "next_cursor") add those to
// the returned map before returning it themselves.
//
// T is constrained to map[string]any (rather than any) because
// internal/core/event's canonical JSON encoder only renders
// map[string]any/[]any/primitives via an exact type switch
// (internal/core/event/canonical.go encodeValue); passing anything else
// would build fine here but fail at print time with an opaque "unsupported
// type" error. The constraint is intentionally tilde-free: `~map[string]any`
// would also accept defined types (e.g. `type Defined map[string]any`),
// which still fail encodeValue's exact match at runtime. Without the tilde,
// only map[string]any and its type aliases satisfy T, so a defined type is
// rejected at compile time instead.
func ItemsMap[T map[string]any](items []T, total int) map[string]any {
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
func PrintList[T map[string]any](items []T, warnings []Warning) error {
	return PrintSuccess(ItemsMap(items, len(items)), warnings)
}

func warningsToAny(warnings []Warning) []any {
	out := make([]any, len(warnings))
	for i, w := range warnings {
		out[i] = map[string]any{"code": w.Code, "message": w.Message}
	}
	return out
}

func printEnvelope(m map[string]any) error {
	out, err := event.Encode(m)
	if err != nil {
		return fmt.Errorf("cliout: encode envelope: %w", err)
	}
	_, err = stdout.Write(out)
	return err
}

// stdout is a seam for tests.
var stdout io.Writer = os.Stdout
