package main

import (
	"context"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/cliout"
)

// The conditions and wording of githive's automatic notifications used to
// live here and again, verbatim, in mcp.go - so the same action produced a
// different notification depending on the client, contradicting
// docs/15-clients.md「全クライアントは機能の意味論を再実装しない」. They now
// live in internal/app/notifyapp (auto.go); what remains here is the shaping
// of its warning strings into the CLI's envelope, and the users-registry
// resolver the mention helper needs injected.
//
// Automatic sync stays on the client side: ADR-0011 has the MCP server
// deliberately not syncing, so the notification and the sync that may follow
// it must not be welded together in the app layer.

// notifyWarnings converts notifyapp's failure strings into CLI warnings.
func notifyWarnings(msgs []string) []cliout.Warning {
	out := make([]cliout.Warning, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, cliout.Warning{Code: "auto_notify_failed", Message: m})
	}
	return out
}

// mentionResolver binds the users registry in dir as notifyapp's resolver.
func mentionResolver(dir string) notifyapp.MentionResolver {
	return func(ctx context.Context, name string) ([]string, error) {
		return usersapp.MentionTargets(ctx, dir, name)
	}
}
