package main

import (
	"context"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ymsaki/githive/internal/app/wikiapp"
)

// registerWikiResource exposes the same JSON as wiki_show without creating a
// worktree, syncing, or changing refs. Reserved expansion permits nested paths;
// URI escapes are decoded once before the app reads the Git tree.
func registerWikiResource(server *mcp.Server, dir string) {
	registerShowResource(server, dir, "wiki", "githive://wiki/{+path}", wikiapp.ErrNotFound, func(ctx context.Context, path string) (any, error) {
		path, err := url.PathUnescape(path)
		if err != nil {
			return nil, err
		}
		content, err := wikiapp.New(dir).Show(ctx, path)
		if err != nil {
			return nil, err
		}
		return wikiShowJSON(path, content), nil
	})
}
