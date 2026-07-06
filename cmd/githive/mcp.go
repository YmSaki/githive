// githive mcp serve: an MCP (Model Context Protocol) server exposing every
// feature's operations as tools, so an Agent can act on a githive-managed
// repository without shelling out to the CLI (docs/15-clients.md「MCP
// サーバー」).
//
// Tools are named and shaped 1:1 with the CLI command tree, and each tool's
// output mirrors the CLI's `--json` "data" field exactly - an Agent that
// falls back to `githive --json` gets the same shapes back
// (docs/00-vision.md's human/Agent parity).
//
// One deliberate deviation from the CLI's defaults: write tools here never
// auto-sync. The CLI syncs after every write unless --no-sync is passed,
// which is fine for a one-shot process, but an MCP server is long-running
// and an Agent may call many tools in a row - syncing (a network round
// trip) after each one would make every call slow and its failure mode
// unclear. Agents call the `sync` tool explicitly instead, matching the
// CLI's --no-sync behavior by default. See docs/adr/0011-mcp-no-auto-sync.md
// for the full rationale, including why this does not change the CLI's
// existing auto-notify behavior (auto-notify events were never part of the
// CLI's auto-sync scope either).
//
// Also note: `verify`'s tool result always reports success (ok:true) at the
// MCP layer even when reports contain issues (ok:false in the data) -
// unlike the CLI, which turns a failed verification into an error envelope
// with exit code 4. MCP tools have no exit-code channel, so failures are
// represented in the structured data instead of as a protocol-level error.
//
// Concurrency: an MCP client may pipeline several tool calls without
// waiting for each response, so two write tools can genuinely execute
// concurrently against the same repository. This is already safe: every
// write goes through internal/app/entitychain.Writer.Append, which takes a
// process-wide, per-repository-directory mutex around its whole
// read-fold-commit-CAS-advance critical section (added for the same
// reason multiple goroutines writing concurrently within one process isn't
// safe on Windows - see that package's writeLocks). Two tool calls
// targeting the same repo simply serialize through that lock; they do not
// race or corrupt each other's writes.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Model Context Protocol server"}
	cmd.AddCommand(newMcpServeCmd())
	return cmd
}

func newMcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve this repository over MCP (stdio transport)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := repoDir()
			if err != nil {
				return err
			}
			server := mcp.NewServer(&mcp.Implementation{Name: "githive", Version: "0.1.0"}, nil)
			registerMcpTools(server, dir)
			registerMcpResources(server, dir)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}

// defaultPageSize/maxPageSize bound paginatedResult (docs/15-clients.md
// 「読み取り系ツールにはページングと絞り込みを持たせ、Agent のコンテキス
// トを浪費させない」). issue/task/chat/notify lists grow without bound
// over a project's lifetime, so they are paginated; users/groups are
// bounded by team size and are not (see users_list).
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// paginate slices an already-sorted-ascending-by-"id" list into one page
// starting just after cursor (an opaque, previously-returned id), returning
// the page and the cursor to pass for the next page (empty if this was the
// last page). Every List() in this codebase already returns items sorted
// this way, so this works generically across issue/task/chat/notify.
func paginate(items []map[string]any, cursor string, limit int) (page []map[string]any, nextCursor string) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	start := 0
	if cursor != "" {
		start = len(items)
		for i, m := range items {
			if id, _ := m["id"].(string); id > cursor {
				start = i
				break
			}
		}
	}
	if start >= len(items) {
		return nil, ""
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page = items[start:end]
	if end < len(items) {
		nextCursor, _ = page[len(page)-1]["id"].(string)
	}
	return page, nextCursor
}

// paginatedResult shapes a paginated list result: `total` is the full
// (unpaginated) match count, `items` is just this page, and `next_cursor`
// is present only when more pages remain.
func paginatedResult(all []map[string]any, cursor string, limit int) map[string]any {
	page, next := paginate(all, cursor, limit)
	anyItems := make([]any, len(page))
	for i, m := range page {
		anyItems[i] = m
	}
	res := map[string]any{"items": anyItems, "total": len(all)}
	if next != "" {
		res["next_cursor"] = next
	}
	return res
}

// autoNotifyWarnings shapes autoNotify's returned failure strings like the
// CLI's cliout.Warning{Code: "auto_notify_failed", ...} entries.
func autoNotifyWarnings(msgs []string) []any {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{"code": "auto_notify_failed", "message": m}
	}
	return out
}

func withWarnings(data map[string]any, warnings []any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	if len(warnings) > 0 {
		data["warnings"] = warnings
	}
	return data
}

// registerMcpTools registers one MCP tool per CLI (sub)command, all bound
// to the single repository at dir.
func registerMcpTools(server *mcp.Server, dir string) {
	registerIssueTools(server, dir)
	registerTaskTools(server, dir)
	registerChatTools(server, dir)
	registerNotifyTools(server, dir)
	registerUsersTools(server, dir)
	registerVerifyAndWhoamiTools(server, dir)
	registerSyncAndStatusTools(server, dir)
}

// ---- issue ----

type issueNewParams struct {
	Title     string   `json:"title" jsonschema:"issue title (required)"`
	Body      string   `json:"body,omitempty" jsonschema:"issue body"`
	Labels    []string `json:"labels,omitempty" jsonschema:"labels to add"`
	Assignees []string `json:"assignees,omitempty" jsonschema:"assignees to add (username or email)"`
}

type issueListParams struct {
	Status   string `json:"status,omitempty" jsonschema:"filter by status"`
	Label    string `json:"label,omitempty" jsonschema:"filter by label"`
	Assignee string `json:"assignee,omitempty" jsonschema:"filter by assignee email"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"resume after this id (from a previous response's next_cursor)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max items to return (default 50, max 200)"`
}

type issueIDParams struct {
	ID string `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
}

type issueCommentParams struct {
	ID         string `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	Message    string `json:"message" jsonschema:"comment body"`
	ReplyTo    string `json:"reply_to,omitempty" jsonschema:"event id this comment replies to"`
	Supersedes string `json:"supersedes,omitempty" jsonschema:"event id of the comment this replaces"`
}

type issueStatusParams struct {
	ID string `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	To string `json:"to" jsonschema:"target status"`
}

type issueEditParams struct {
	ID    string  `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	Title *string `json:"title,omitempty" jsonschema:"new title; omit to leave unchanged"`
	Body  *string `json:"body,omitempty" jsonschema:"new body; omit to leave unchanged"`
}

type issueLabelParams struct {
	ID     string   `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	Add    []string `json:"add,omitempty" jsonschema:"labels to add"`
	Remove []string `json:"remove,omitempty" jsonschema:"labels to remove"`
}

type issueAssignParams struct {
	ID     string   `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	Add    []string `json:"add,omitempty" jsonschema:"assignees to add (username or email)"`
	Remove []string `json:"remove,omitempty" jsonschema:"assignees to remove (username or email)"`
}

type issueLinkParams struct {
	ID        string `json:"id" jsonschema:"issue id or an unambiguous >=8 char prefix"`
	LinkedID  string `json:"linked_id" jsonschema:"the id of the entity to link to"`
	Rel       string `json:"rel,omitempty" jsonschema:"link relation: task/issue/chat (default task)"`
	Unlinking bool   `json:"remove,omitempty" jsonschema:"remove the link instead of adding it"`
}

func registerIssueTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "issue_new", Description: "Create a new issue"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueNewParams) (*mcp.CallToolResult, map[string]any, error) {
			resolved, err := resolveUserRefs(ctx, dir, args.Assignees)
			if err != nil {
				return nil, nil, err
			}
			id, err := issueapp.New(dir).NewIssue(ctx, args.Title, args.Body, args.Labels, resolved)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_list", Description: "List issues (paginated)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueListParams) (*mcp.CallToolResult, map[string]any, error) {
			// Resolve assignee the same way issue_new/issue_assign resolve
			// their person args, so filtering by the username you just
			// assigned someone with actually matches (leniently: an
			// unknown name just filters to nothing, as it always did).
			assignee := resolveUserRefLenient(ctx, dir, args.Assignee)
			items, err := issueapp.New(dir).List(ctx, issueapp.ListFilter{Status: args.Status, Label: args.Label, Assignee: assignee})
			if err != nil {
				return nil, nil, err
			}
			return nil, paginatedResult(items, args.Cursor, args.Limit), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_show", Description: "Show an issue's meta, body, and comments"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueIDParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			comments := make([]any, len(show.Comments))
			for i, c := range show.Comments {
				comments[i] = c
			}
			return nil, map[string]any{"meta": show.Meta, "body": show.Body, "comments": comments}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_comment", Description: "Add a comment to an issue"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueCommentParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Comment(ctx, id, args.Message, args.ReplyTo, args.Supersedes); err != nil {
				return nil, nil, err
			}
			var warnStrings []string
			if args.ReplyTo != "" {
				if show, showErr := svc.Show(ctx, id); showErr == nil {
					selfEmail := ""
					if sig, sigErr := identity.Resolve(ctx, dir); sigErr == nil {
						selfEmail = sig.Email
					}
					for _, c := range show.Comments {
						if c["id"] == args.ReplyTo {
							if author, ok := c["author"].(string); ok && author != "" && author != selfEmail {
								warnStrings = append(warnStrings, autoNotify(ctx, dir, "user:"+author,
									fmt.Sprintf("issue %s にコメントが返信されました", shortID(id)),
									map[string]any{"kind": "issue", "id": id})...)
							}
							break
						}
					}
				}
			}
			return nil, withWarnings(nil, autoNotifyWarnings(warnStrings)), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_status", Description: "Transition an issue's status"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueStatusParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			ok, err := svc.Status(ctx, id, args.To)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"applied": ok}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_edit", Description: "Edit an issue's title/body"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueEditParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Edit(ctx, id, args.Title, args.Body); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_label", Description: "Add/remove an issue's labels"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueLabelParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Label(ctx, id, args.Add, args.Remove); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_assign", Description: "Add/remove an issue's assignees"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueAssignParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			resolvedAdd, err := resolveUserRefs(ctx, dir, args.Add)
			if err != nil {
				return nil, nil, err
			}
			resolvedRemove, err := resolveUserRefs(ctx, dir, args.Remove)
			if err != nil {
				return nil, nil, err
			}

			before, showErr := svc.Show(ctx, id)
			alreadyAssigned := map[string]bool{}
			if showErr == nil {
				for _, a := range asStringSliceAny(before.Meta["assignees"]) {
					alreadyAssigned[a] = true
				}
			}

			if err := svc.Assign(ctx, id, resolvedAdd, resolvedRemove); err != nil {
				return nil, nil, err
			}

			selfEmail := ""
			if sig, sigErr := identity.Resolve(ctx, dir); sigErr == nil {
				selfEmail = sig.Email
			}
			var warnStrings []string
			for _, assignee := range resolvedAdd {
				if assignee == selfEmail || alreadyAssigned[assignee] {
					continue
				}
				warnStrings = append(warnStrings, autoNotify(ctx, dir, "user:"+assignee,
					fmt.Sprintf("issue %s の担当になりました", shortID(id)),
					map[string]any{"kind": "issue", "id": id})...)
			}
			return nil, withWarnings(nil, autoNotifyWarnings(warnStrings)), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "issue_link", Description: "Link an issue to another entity"},
		func(ctx context.Context, req *mcp.CallToolRequest, args issueLinkParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := issueapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			rel := args.Rel
			if rel == "" {
				rel = "task"
			}
			if err := svc.Link(ctx, id, rel, args.LinkedID, args.Unlinking); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})
}

// ---- task ----

type taskNewParams struct {
	Title    string `json:"title" jsonschema:"task title (required)"`
	Body     string `json:"body,omitempty" jsonschema:"task body"`
	Owner    string `json:"owner,omitempty" jsonschema:"owner (username or email; defaults to actor)"`
	Due      string `json:"due,omitempty" jsonschema:"due date"`
	Priority string `json:"priority,omitempty" jsonschema:"priority"`
}

type taskListParams struct {
	Status string `json:"status,omitempty" jsonschema:"filter by status"`
	Owner  string `json:"owner,omitempty" jsonschema:"filter by owner email"`
	Mine   bool   `json:"mine,omitempty" jsonschema:"only tasks owned by the actor"`
	Cursor string `json:"cursor,omitempty" jsonschema:"resume after this id (from a previous response's next_cursor)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max items to return (default 50, max 200)"`
}

type taskIDParams struct {
	ID string `json:"id" jsonschema:"task id or an unambiguous >=8 char prefix"`
}

type taskStatusParams struct {
	ID      string `json:"id" jsonschema:"task id or an unambiguous >=8 char prefix"`
	To      string `json:"to" jsonschema:"target status"`
	Message string `json:"message,omitempty" jsonschema:"note to attach to this transition"`
}

type taskNoteParams struct {
	ID      string `json:"id" jsonschema:"task id or an unambiguous >=8 char prefix"`
	Message string `json:"message" jsonschema:"note body"`
}

type taskReassignParams struct {
	ID    string `json:"id" jsonschema:"task id or an unambiguous >=8 char prefix"`
	Owner string `json:"owner" jsonschema:"new owner (username or email)"`
}

func registerTaskTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "task_new", Description: "Create a new task"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskNewParams) (*mcp.CallToolResult, map[string]any, error) {
			owner, err := resolveUserRef(ctx, dir, args.Owner)
			if err != nil {
				return nil, nil, err
			}
			id, err := taskapp.New(dir).NewTask(ctx, args.Title, args.Body, owner, args.Due, args.Priority)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "task_list", Description: "List tasks (paginated)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskListParams) (*mcp.CallToolResult, map[string]any, error) {
			// See issue_list's comment: resolve owner the same way task_new/
			// task_reassign resolve it, leniently.
			owner := resolveUserRefLenient(ctx, dir, args.Owner)
			filter := taskapp.ListFilter{Status: args.Status, Owner: owner, Mine: args.Mine}
			if args.Mine {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return nil, nil, err
				}
				filter.ActorEmail = sig.Email
			}
			items, err := taskapp.New(dir).List(ctx, filter)
			if err != nil {
				return nil, nil, err
			}
			return nil, paginatedResult(items, args.Cursor, args.Limit), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "task_show", Description: "Show a task's meta, body, and notes"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskIDParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := taskapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			notes := make([]any, len(show.Notes))
			for i, n := range show.Notes {
				notes[i] = n
			}
			return nil, map[string]any{"meta": show.Meta, "body": show.Body, "notes": notes}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "task_status", Description: "Transition a task's status"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskStatusParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := taskapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			ok, err := svc.Status(ctx, id, args.To, args.Message)
			if err != nil {
				return nil, nil, err
			}
			var warnStrings []string
			if ok && args.To == "done" {
				// Notify the task's creator (docs/features/notify.md「自動通知」).
				if show, showErr := svc.Show(ctx, id); showErr == nil {
					createdBy, _ := show.Meta["created_by"].(string)
					sig, sigErr := identity.Resolve(ctx, dir)
					if createdBy != "" && (sigErr != nil || createdBy != sig.Email) {
						warnStrings = append(warnStrings, autoNotify(ctx, dir, "user:"+createdBy,
							fmt.Sprintf("task %s が done になりました", shortID(id)),
							map[string]any{"kind": "task", "id": id})...)
					}
				}
			}
			return nil, withWarnings(map[string]any{"applied": ok}, autoNotifyWarnings(warnStrings)), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "task_note", Description: "Add a note to a task"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskNoteParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := taskapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Note(ctx, id, args.Message); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "task_reassign", Description: "Reassign a task's owner"},
		func(ctx context.Context, req *mcp.CallToolRequest, args taskReassignParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := taskapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			resolvedOwner, err := resolveUserRef(ctx, dir, args.Owner)
			if err != nil {
				return nil, nil, err
			}

			before, showErr := svc.Show(ctx, id)
			previousOwner := ""
			if showErr == nil {
				previousOwner, _ = before.Meta["owner"].(string)
			}

			if err := svc.Reassign(ctx, id, resolvedOwner); err != nil {
				return nil, nil, err
			}

			selfEmail := ""
			if sig, sigErr := identity.Resolve(ctx, dir); sigErr == nil {
				selfEmail = sig.Email
			}
			var warnStrings []string
			if resolvedOwner != selfEmail && resolvedOwner != previousOwner {
				warnStrings = append(warnStrings, autoNotify(ctx, dir, "user:"+resolvedOwner,
					fmt.Sprintf("task %s の担当になりました", shortID(id)),
					map[string]any{"kind": "task", "id": id})...)
			}
			return nil, withWarnings(nil, autoNotifyWarnings(warnStrings)), nil
		})
}

// ---- chat ----

type chatNewParams struct {
	Title string `json:"title" jsonschema:"thread title (required)"`
	Body  string `json:"body,omitempty" jsonschema:"first message body"`
}

type chatListParams struct {
	Status string `json:"status,omitempty" jsonschema:"filter by status"`
	Cursor string `json:"cursor,omitempty" jsonschema:"resume after this id (from a previous response's next_cursor)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max items to return (default 50, max 200)"`
}

type chatIDParams struct {
	ID string `json:"id" jsonschema:"chat thread id or an unambiguous >=8 char prefix"`
}

type chatPostParams struct {
	ID         string `json:"id" jsonschema:"chat thread id or an unambiguous >=8 char prefix"`
	Body       string `json:"body" jsonschema:"message body"`
	ReplyTo    string `json:"reply_to,omitempty" jsonschema:"event id this message replies to"`
	Supersedes string `json:"supersedes,omitempty" jsonschema:"event id of the message this replaces"`
}

type chatEditMetaParams struct {
	ID     string `json:"id" jsonschema:"chat thread id or an unambiguous >=8 char prefix"`
	Title  string `json:"title,omitempty" jsonschema:"new title; omit to leave unchanged"`
	Status string `json:"status,omitempty" jsonschema:"new status; omit to leave unchanged"`
}

func registerChatTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "chat_new", Description: "Create a new chat thread"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatNewParams) (*mcp.CallToolResult, map[string]any, error) {
			id, err := chatapp.New(dir).NewThread(ctx, args.Title, args.Body)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "chat_list", Description: "List chat threads (paginated)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatListParams) (*mcp.CallToolResult, map[string]any, error) {
			items, err := chatapp.New(dir).List(ctx, chatapp.ListFilter{Status: args.Status})
			if err != nil {
				return nil, nil, err
			}
			return nil, paginatedResult(items, args.Cursor, args.Limit), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "chat_show", Description: "Show a chat thread's meta and messages"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatIDParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := chatapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			show, err := svc.Show(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			messages := make([]any, len(show.Messages))
			for i, m := range show.Messages {
				messages[i] = m
			}
			return nil, map[string]any{"meta": show.Meta, "messages": messages}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "chat_post", Description: "Post a message to a chat thread"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatPostParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := chatapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Post(ctx, id, args.Body, args.ReplyTo, args.Supersedes); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "chat_archive", Description: "Archive a chat thread"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatIDParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := chatapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.Archive(ctx, id); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "chat_edit_meta", Description: "Edit a chat thread's title/status"},
		func(ctx context.Context, req *mcp.CallToolRequest, args chatEditMetaParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := chatapp.New(dir)
			id, err := svc.ResolveID(ctx, args.ID)
			if err != nil {
				return nil, nil, err
			}
			if err := svc.EditMeta(ctx, id, args.Title, args.Status); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})
}

// ---- notify ----

type notifySourceParams struct {
	Kind string `json:"kind" jsonschema:"source entity kind, e.g. task/issue"`
	ID   string `json:"id" jsonschema:"source entity id"`
}

type notifyPostParams struct {
	To      []string            `json:"to" jsonschema:"target(s): user:<username-or-email>, group:<name>, or all"`
	Title   string              `json:"title" jsonschema:"notification title (required)"`
	Message string              `json:"message,omitempty" jsonschema:"notification body"`
	Source  *notifySourceParams `json:"source,omitempty" jsonschema:"source entity this notification references"`
}

type notifyListParams struct {
	Unread bool   `json:"unread,omitempty" jsonschema:"only unread notifications addressed to the actor"`
	Cursor string `json:"cursor,omitempty" jsonschema:"resume after this id (from a previous response's next_cursor)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max items to return (default 50, max 200)"`
}

type notifyAckParams struct {
	IDs []string `json:"ids,omitempty" jsonschema:"event ids to acknowledge"`
	All bool     `json:"all,omitempty" jsonschema:"acknowledge every unread notification addressed to the actor"`
}

func registerNotifyTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "notify_post", Description: "Post a notification"},
		func(ctx context.Context, req *mcp.CallToolRequest, args notifyPostParams) (*mcp.CallToolResult, map[string]any, error) {
			resolvedTo, err := resolveNotifyTargets(ctx, dir, args.To)
			if err != nil {
				return nil, nil, err
			}
			var source map[string]any
			if args.Source != nil {
				source = map[string]any{"kind": args.Source.Kind, "id": args.Source.ID}
			}
			id, err := notifyapp.New(dir).Post(ctx, resolvedTo, args.Title, args.Message, source, "")
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "notify_list", Description: "List notifications (paginated)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args notifyListParams) (*mcp.CallToolResult, map[string]any, error) {
			filter := notifyapp.ListFilter{UnreadOnly: args.Unread}
			if args.Unread {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return nil, nil, err
				}
				filter.ActorEmail = sig.Email
			}
			items, err := notifyapp.New(dir).List(ctx, filter)
			if err != nil {
				return nil, nil, err
			}
			return nil, paginatedResult(items, args.Cursor, args.Limit), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "notify_ack", Description: "Acknowledge notifications"},
		func(ctx context.Context, req *mcp.CallToolRequest, args notifyAckParams) (*mcp.CallToolResult, map[string]any, error) {
			svc := notifyapp.New(dir)
			ids := args.IDs
			if args.All {
				sig, err := identity.Resolve(ctx, dir)
				if err != nil {
					return nil, nil, err
				}
				unread, err := svc.List(ctx, notifyapp.ListFilter{UnreadOnly: true, ActorEmail: sig.Email})
				if err != nil {
					return nil, nil, err
				}
				ids = nil
				for _, m := range unread {
					if id, ok := m["id"].(string); ok {
						ids = append(ids, id)
					}
				}
			}
			if len(ids) == 0 {
				if args.All {
					// An Agent acking defensively ("clear my inbox") with
					// nothing unread is a no-op success, not an error.
					return nil, map[string]any{"acked": []any{}}, nil
				}
				return nil, nil, fmt.Errorf("notify_ack: no event ids given (pass ids or all=true)")
			}
			if err := svc.Ack(ctx, ids); err != nil {
				return nil, nil, err
			}
			anyIDs := make([]any, len(ids))
			for i, id := range ids {
				anyIDs[i] = id
			}
			return nil, map[string]any{"acked": anyIDs}, nil
		})
}

// ---- users ----

type usersAddParams struct {
	Name    string   `json:"name" jsonschema:"username (required)"`
	Display string   `json:"display,omitempty" jsonschema:"display name"`
	Email   string   `json:"email,omitempty" jsonschema:"email address"`
	Kind    string   `json:"kind,omitempty" jsonschema:"human or agent"`
	Roles   []string `json:"roles,omitempty" jsonschema:"roles to set, e.g. admin"`
	Agent   bool     `json:"agent,omitempty" jsonschema:"set up a dedicated Agent identity (keypair + registry entry) instead of a plain user_set"`
	Project string   `json:"project,omitempty" jsonschema:"project name for the agent's minted email (agent mode only; default: read from meta/config)"`
	KeyDir  string   `json:"key_dir,omitempty" jsonschema:"directory to write the agent's SSH keypair into (agent mode only; default ~/.ssh)"`
}

type usersKeyParams struct {
	Name string `json:"name" jsonschema:"username (required)"`
	Pub  string `json:"pub" jsonschema:"public key, literal or a path to a file readable by the server"`
}

type usersGroupSetParams struct {
	Name        string   `json:"name" jsonschema:"group name (required)"`
	Members     []string `json:"members,omitempty" jsonschema:"group members (usernames)"`
	Description string   `json:"description,omitempty" jsonschema:"group description"`
}

type usersGroupRemoveParams struct {
	Name string `json:"name" jsonschema:"group name (required)"`
}

type usersPolicySetParams struct {
	Rules   []any  `json:"rules" jsonschema:"policy rules array, see docs/features/users.md policy.json"`
	Default string `json:"default" jsonschema:"default decision when no rule matches refs/projects/**: allow or deny"`
}

func registerUsersTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "users_add", Description: "Add or update a registry user (or set up a dedicated Agent identity with agent=true)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersAddParams) (*mcp.CallToolResult, map[string]any, error) {
			if args.Agent {
				setup, err := usersapp.AddAgent(ctx, dir, args.Name, args.Project, args.Email, args.KeyDir)
				if err != nil {
					return nil, nil, err
				}
				return nil, map[string]any{
					"username":   setup.Username,
					"email":      setup.Email,
					"public_key": setup.PublicKey,
					"pub_line":   setup.PubLine,
				}, nil
			}
			if err := usersapp.New(dir).AddUser(ctx, args.Name, args.Display, args.Email, args.Kind, args.Roles); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "users_list", Description: "List registry users, groups, and policy"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]any, error) {
			users, groups, policy, err := usersapp.New(dir).List(ctx)
			if err != nil {
				return nil, nil, err
			}
			anyUsers := make([]any, len(users))
			for i, u := range users {
				anyUsers[i] = u
			}
			anyGroups := make([]any, len(groups))
			for i, g := range groups {
				anyGroups[i] = g
			}
			var policyAny any
			if policy != nil {
				policyAny = policy
			}
			return nil, map[string]any{"users": anyUsers, "groups": anyGroups, "policy": policyAny}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "users_key_add", Description: "Register an SSH public key for a user"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersKeyParams) (*mcp.CallToolResult, map[string]any, error) {
			// Unlike the CLI's --pub (which also accepts a file path),
			// pub here must be a literal key line - see
			// looksLikeSSHPublicKey's doc comment for why.
			if !looksLikeSSHPublicKey(args.Pub) {
				return nil, nil, fmt.Errorf("users_key_add: pub must be a literal SSH public key line (\"<type> <base64-data> [comment]\"), not a file path")
			}
			if err := usersapp.New(dir).KeyAdd(ctx, args.Name, args.Pub); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "users_key_revoke", Description: "Revoke a user's SSH public key"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersKeyParams) (*mcp.CallToolResult, map[string]any, error) {
			if !looksLikeSSHPublicKey(args.Pub) {
				return nil, nil, fmt.Errorf("users_key_revoke: pub must be a literal SSH public key line (\"<type> <base64-data> [comment]\"), not a file path")
			}
			if err := usersapp.New(dir).KeyRevoke(ctx, args.Name, args.Pub); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "users_group_set", Description: "Create or replace a group"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersGroupSetParams) (*mcp.CallToolResult, map[string]any, error) {
			if err := usersapp.New(dir).GroupSet(ctx, args.Name, args.Members, args.Description); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "users_group_remove", Description: "Remove a group"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersGroupRemoveParams) (*mcp.CallToolResult, map[string]any, error) {
			if err := usersapp.New(dir).GroupRemove(ctx, args.Name); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})

	// users_policy_set replaces the CLI's `users policy edit` (an
	// interactive $EDITOR flow that has no meaning over an MCP stdio
	// transport) with a tool that takes the whole policy directly - still
	// a whole-object replace per docs/features/users.md「policy は部分編集
	// イベントにせず全置換とする」.
	mcp.AddTool(server, &mcp.Tool{Name: "users_policy_set", Description: "Replace the access policy wholesale"},
		func(ctx context.Context, req *mcp.CallToolRequest, args usersPolicySetParams) (*mcp.CallToolResult, map[string]any, error) {
			if err := usersapp.New(dir).PolicySet(ctx, args.Rules, args.Default); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{}, nil
		})
}

// ---- verify / whoami ----

type verifyParams struct {
	Ref string `json:"ref,omitempty" jsonschema:"verify only this ref (default: every verifiable ref)"`
}

func registerVerifyAndWhoamiTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "verify", Description: "Verify commit signatures and chain integrity (docs/11-security.md)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args verifyParams) (*mcp.CallToolResult, map[string]any, error) {
			trustRootPin, err := verifyapp.TrustRootPin(ctx, dir)
			if err != nil {
				return nil, nil, err
			}
			var reports []verifyapp.RefReport
			if args.Ref != "" {
				report, err := verifyapp.VerifyRef(ctx, dir, args.Ref, trustRootPin)
				if err != nil {
					return nil, nil, err
				}
				reports = []verifyapp.RefReport{*report}
			} else {
				reports, err = verifyapp.VerifyAll(ctx, dir, trustRootPin)
				if err != nil {
					return nil, nil, err
				}
			}
			ok := true
			for _, r := range reports {
				if !r.OK() {
					ok = false
					break
				}
			}
			return nil, map[string]any{"ok": ok, "reports": reportsToAny(reports)}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "Show the identity githive will record as actor, and its registry status"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]any, error) {
			sig, err := identity.Resolve(ctx, dir)
			if err != nil {
				return nil, nil, err
			}
			user, keys := matchRegistryUser(ctx, dir, sig.Email)
			data := map[string]any{"name": sig.Name, "email": sig.Email}
			if user != nil {
				data["username"] = user["username"]
				data["registered"] = true
				anyKeys := make([]any, len(keys))
				for i, k := range keys {
					anyKeys[i] = k
				}
				data["keys"] = anyKeys
			} else {
				data["registered"] = false
			}
			return nil, data, nil
		})
}

// ---- sync / status ----

type syncParams struct {
	Remote string   `json:"remote,omitempty" jsonschema:"sync remote name (default origin)"`
	Kinds  []string `json:"kinds,omitempty" jsonschema:"feature list to sync (default: every supported feature)"`
}

func registerSyncAndStatusTools(server *mcp.Server, dir string) {
	mcp.AddTool(server, &mcp.Tool{Name: "sync", Description: "fetch -> merge -> push every githive ref (or a kinds subset)"},
		func(ctx context.Context, req *mcp.CallToolRequest, args syncParams) (*mcp.CallToolResult, map[string]any, error) {
			remote := args.Remote
			if remote == "" {
				remote = "origin"
			}
			refs, err := refsToSync(ctx, dir, remote, args.Kinds)
			if err != nil {
				return nil, nil, err
			}
			results, err := syncapp.Sync(ctx, dir, remote, refs, syncapp.DefaultRetries)
			if err != nil {
				return nil, nil, err
			}
			items := make([]any, len(results))
			for i, r := range results {
				items[i] = map[string]any{"ref": r.Ref, "action": string(r.Action)}
			}
			return nil, map[string]any{"items": items, "total": len(items)}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "status", Description: "Summarize unpushed refs, unread notifications, and the actor's doing tasks"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]any, error) {
			unpushed, err := unpushedRefs(ctx, dir)
			if err != nil {
				return nil, nil, err
			}
			sig, err := identity.Resolve(ctx, dir)
			if err != nil {
				return nil, nil, err
			}
			unread, err := notifyapp.New(dir).List(ctx, notifyapp.ListFilter{UnreadOnly: true, ActorEmail: sig.Email})
			if err != nil {
				return nil, nil, err
			}
			doing, err := taskapp.New(dir).List(ctx, taskapp.ListFilter{Status: "doing", Mine: true, ActorEmail: sig.Email})
			if err != nil {
				return nil, nil, err
			}
			anyUnpushed := make([]any, len(unpushed))
			for i, r := range unpushed {
				anyUnpushed[i] = r
			}
			doingItems := make([]any, len(doing))
			for i, m := range doing {
				doingItems[i] = m
			}
			return nil, map[string]any{
				"unpushed_refs":  anyUnpushed,
				"unread_notify":  len(unread),
				"my_doing_tasks": doingItems,
			}, nil
		})
}

// registerMcpResources publishes githive://<feature>/<id> resources
// (docs/15-clients.md「リソースとして githive://issue/<id> 等の URI を公開
// し、wiki の githive: リンクと対応させる」), each returning the same JSON
// shape as its *_show tool.
func registerMcpResources(server *mcp.Server, dir string) {
	registerShowResource(server, dir, "issue", "githive://issue/{id}", issueapp.ErrNotFound, func(ctx context.Context, id string) (any, error) {
		svc := issueapp.New(dir)
		resolved, err := svc.ResolveID(ctx, id)
		if err != nil {
			return nil, err
		}
		show, err := svc.Show(ctx, resolved)
		if err != nil {
			return nil, err
		}
		comments := make([]any, len(show.Comments))
		for i, c := range show.Comments {
			comments[i] = c
		}
		return map[string]any{"meta": show.Meta, "body": show.Body, "comments": comments}, nil
	})
	registerShowResource(server, dir, "task", "githive://task/{id}", taskapp.ErrNotFound, func(ctx context.Context, id string) (any, error) {
		svc := taskapp.New(dir)
		resolved, err := svc.ResolveID(ctx, id)
		if err != nil {
			return nil, err
		}
		show, err := svc.Show(ctx, resolved)
		if err != nil {
			return nil, err
		}
		notes := make([]any, len(show.Notes))
		for i, n := range show.Notes {
			notes[i] = n
		}
		return map[string]any{"meta": show.Meta, "body": show.Body, "notes": notes}, nil
	})
	registerShowResource(server, dir, "chat", "githive://chat/{id}", chatapp.ErrNotFound, func(ctx context.Context, id string) (any, error) {
		svc := chatapp.New(dir)
		resolved, err := svc.ResolveID(ctx, id)
		if err != nil {
			return nil, err
		}
		show, err := svc.Show(ctx, resolved)
		if err != nil {
			return nil, err
		}
		messages := make([]any, len(show.Messages))
		for i, m := range show.Messages {
			messages[i] = m
		}
		return map[string]any{"meta": show.Meta, "messages": messages}, nil
	})
}

// registerShowResource wires one githive://<feature>/<id> resource
// template. notFoundErr is the feature's own "no such entity" sentinel
// (e.g. issueapp.ErrNotFound) - only that specific error is translated to
// the MCP protocol's ResourceNotFoundError; anything else (an ambiguous id
// prefix, a git-level failure) is returned as-is, so a real failure isn't
// misreported as "this resource doesn't exist".
func registerShowResource(server *mcp.Server, dir, feature, uriTemplate string, notFoundErr error, show func(ctx context.Context, id string) (any, error)) {
	prefix := "githive://" + feature + "/"
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        feature,
		Description: "githive " + feature + " (meta + full content), by id",
		MIMEType:    "application/json",
		URITemplate: uriTemplate,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		id, ok := strings.CutPrefix(uri, prefix)
		if !ok || id == "" {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		data, err := show(ctx, id)
		if err != nil {
			if errors.Is(err, notFoundErr) {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			return nil, err
		}
		encoded, err := event.Encode(data)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(encoded)}},
		}, nil
	})
}
