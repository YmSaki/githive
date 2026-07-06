# githive Claude Code plugin

Packages `githive mcp serve` and a usage skill as a Claude Code plugin
(docs/15-clients.md「MCP サーバー」「配布形態」).

## Prerequisites

This plugin does not bundle the `githive` binary. Install it separately so
it's on `PATH`:

```sh
go install github.com/ymsaki/githive/cmd/githive@latest
```

## Contents

- `.claude-plugin/plugin.json` - plugin manifest.
- `.mcp.json` - registers the `githive` MCP server, launched as
  `githive mcp serve --repo "${CLAUDE_PROJECT_DIR}"` so it always operates
  on the project the session is rooted in regardless of the server
  process's working directory.
- `skills/githive-usage/SKILL.md` - teaches an Agent githive's operational
  conventions: check `status` first, prefer paginated reads, call `sync`
  explicitly (writes don't auto-sync, see
  [docs/adr/0011-mcp-no-auto-sync.md](../../../docs/adr/0011-mcp-no-auto-sync.md)),
  resolve people by username or email, and use `verify` for trust questions.

## Versioning

`plugin.json`'s `version` is currently a hand-set placeholder tracking the
project's roadmap milestone naming (v0.1, see docs/13-roadmap.md「MVP」).
Regenerating it automatically from githive's own release version is future
work for the project's release CI (docs/15-clients.md「配布形態」) - not yet
implemented.

## Not yet done

A Codex plugin package (docs/13-roadmap.md P5's other stated deliverable)
does not exist yet.
