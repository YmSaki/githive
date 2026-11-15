// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hook implements githive-forge's git server hooks. It only does
// hook I/O - reading git's input, reporting refusals on stderr (which git
// relays to the pushing client), and choosing the exit status. The
// acceptance rules themselves live in the MIT app layer
// (internal/app/receiveapp, ADR-0035).
package hook

import (
	"context"
	"fmt"
	"io"

	"github.com/ymsaki/githive/internal/app/receiveapp"
)

// Exit statuses of RunPreReceive. Any non-zero status makes git refuse the
// whole push (githooks(5) pre-receive).
const (
	ExitAccept = 0
	ExitReject = 1
	// ExitError means the hook could not decide (bad input, bad config,
	// git failure). The push is refused rather than accepted unchecked.
	ExitError = 2
)

// RunPreReceive checks the ref updates git writes to stdin for the
// repository at dir and returns the hook's exit status.
func RunPreReceive(ctx context.Context, dir string, cfg receiveapp.Config, stdin io.Reader, stderr io.Writer) int {
	if err := receiveapp.ValidateConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "githive-forge: pre-receive misconfigured: %v\n", err)
		return ExitError
	}
	updates, err := receiveapp.ParseUpdates(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "githive-forge: pre-receive: %v\n", err)
		return ExitError
	}
	rejections, err := receiveapp.New(dir).Check(ctx, cfg, updates)
	if err != nil {
		fmt.Fprintf(stderr, "githive-forge: pre-receive could not check the push, refusing it: %v\n", err)
		return ExitError
	}
	if len(rejections) == 0 {
		return ExitAccept
	}
	for _, r := range rejections {
		fmt.Fprintf(stderr, "githive-forge: rejected %s [%s]: %s\n", r.Ref, r.Code, r.Message)
	}
	return ExitReject
}
