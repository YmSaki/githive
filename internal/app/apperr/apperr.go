// Package apperr holds error types shared by more than one
// internal/app/* feature package.
//
// AmbiguousIDError started as three separately-declared, identically-shaped
// types in issueapp, taskapp and chatapp. Once a third feature needed the
// same shape, .claude/rules/go-layering.md's "共通化は2例目まで複製、3例目で
// 抽出" policy calls for extracting it here, so cmd/githive's error
// classifier (docs/10-cli-spec.md「終了コード」) only has to catch one type
// with errors.As instead of one per feature.
package apperr

import (
	"fmt"
	"strings"
)

// AmbiguousIDError is returned by a feature service's ResolveID when a
// short ID prefix matches more than one entity of that feature
// (docs/10-cli-spec.md「ID の入力解決」: 「曖昧なら候補一覧を表示して終了コード
// 2。--json 時は candidates を error.data に入れる」). Its Error() message
// includes the full candidate list so every consumer that only has access
// to the plain error text - the CLI's human-readable output and an MCP
// tool's IsError text content alike (docs/15-clients.md「MCP サーバー」) -
// still surfaces the candidates, not just their count.
type AmbiguousIDError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousIDError) Error() string {
	return fmt.Sprintf("ambiguous id prefix %q matches %d candidates: %s", e.Prefix, len(e.Candidates), strings.Join(e.Candidates, ", "))
}
