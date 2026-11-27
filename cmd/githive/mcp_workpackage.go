package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ymsaki/githive/internal/app/workpackageapp"
)

// MCP wp_* tools mirror the `githive wp` CLI through the same app service.
// Per ADR-0011 none of them fetch or sync (wp_start included).

type wpIDParams struct {
	ID string `json:"id" jsonschema:"workpackage id or an unambiguous >=8 char prefix"`
}

type wpNewParams struct {
	Title     string   `json:"title" jsonschema:"title (required)"`
	Body      *string  `json:"body,omitempty" jsonschema:"acceptance criteria / body"`
	Kind      string   `json:"kind,omitempty" jsonschema:"work or container (default work)"`
	Priority  string   `json:"priority,omitempty" jsonschema:"low, normal or high"`
	Parent    string   `json:"parent,omitempty" jsonschema:"WBS parent workpackage id"`
	DependsOn []string `json:"depends_on,omitempty" jsonschema:"workpackage ids this one depends on"`
}

type wpEditParams struct {
	ID          string  `json:"id" jsonschema:"workpackage id"`
	Title       *string `json:"title,omitempty" jsonschema:"new title"`
	Body        *string `json:"body,omitempty" jsonschema:"new body (changes acceptance_version)"`
	Priority    *string `json:"priority,omitempty" jsonschema:"low, normal or high"`
	Parent      *string `json:"parent,omitempty" jsonschema:"new WBS parent id"`
	ClearParent bool    `json:"clear_parent,omitempty" jsonschema:"remove the WBS parent"`
}

type wpFindingParams struct {
	ParentID string  `json:"parent_id" jsonschema:"workpackage the finding is about"`
	Title    string  `json:"title" jsonschema:"title (required)"`
	Body     *string `json:"body,omitempty" jsonschema:"body"`
	SubmitID string  `json:"submit_id,omitempty" jsonschema:"submission (default: current)"`
	Parent   string  `json:"parent,omitempty" jsonschema:"WBS parent id (default: none)"`
}

type wpListParams struct {
	Ready    bool   `json:"ready,omitempty" jsonschema:"only Ready"`
	Mine     bool   `json:"mine,omitempty" jsonschema:"only my winning claims"`
	Blocked  bool   `json:"blocked,omitempty" jsonschema:"only Blocked"`
	InReview bool   `json:"in_review,omitempty" jsonschema:"only InReview"`
	Parent   string `json:"parent,omitempty" jsonschema:"only children of this WBS parent"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"resume after this id"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max items (default 50, max 200)"`
}

type wpStartParams struct {
	ID     string `json:"id" jsonschema:"workpackage id"`
	Branch string `json:"branch,omitempty" jsonschema:"branch hint"`
	Lease  string `json:"lease,omitempty" jsonschema:"lease duration such as 24h, at least 1ms (default 24h)"`
}

type wpRenewParams struct {
	ID      string `json:"id" jsonschema:"workpackage id"`
	ClaimID string `json:"claim_id,omitempty" jsonschema:"claim id (default: your only live claim)"`
	Lease   string `json:"lease,omitempty" jsonschema:"new lease duration from now, at least 1ms (default 24h)"`
	Branch  string `json:"branch,omitempty" jsonschema:"new branch hint"`
}

type wpReleaseParams struct {
	ID      string `json:"id" jsonschema:"workpackage id"`
	ClaimID string `json:"claim_id,omitempty" jsonschema:"claim id (default: your only unreleased claim)"`
	Message string `json:"message,omitempty" jsonschema:"note"`
}

type wpSubmitParams struct {
	ID        string   `json:"id" jsonschema:"workpackage id"`
	Commit    *string  `json:"commit,omitempty" jsonschema:"full commit OID (default HEAD)"`
	NoCommit  bool     `json:"no_commit,omitempty" jsonschema:"submit without a commit"`
	Branch    string   `json:"branch,omitempty" jsonschema:"branch hint"`
	PRURL     string   `json:"pr_url,omitempty" jsonschema:"pull request URL"`
	Reviewers []string `json:"reviewers,omitempty" jsonschema:"usernames or emails"`
	Message   string   `json:"message,omitempty" jsonschema:"note"`
}

type wpAcceptParams struct {
	ID       string `json:"id" jsonschema:"workpackage id"`
	SubmitID string `json:"submit_id,omitempty" jsonschema:"submission (default: current)"`
	Message  string `json:"message,omitempty" jsonschema:"note"`
}

type wpWaiveParams struct {
	ID      string `json:"id" jsonschema:"workpackage id"`
	Target  string `json:"target" jsonschema:"finding workpackage id"`
	Message string `json:"message" jsonschema:"reason (required)"`
}

type wpMessageParams struct {
	ID      string `json:"id" jsonschema:"workpackage id"`
	Message string `json:"message,omitempty" jsonschema:"note body / cancel reason"`
}

type wpDepParams struct {
	ID     string `json:"id" jsonschema:"workpackage id"`
	Target string `json:"target" jsonschema:"dependency workpackage id"`
	Remove bool   `json:"remove,omitempty" jsonschema:"remove instead of add"`
}

type wpLinkParams struct {
	ID       string `json:"id" jsonschema:"workpackage id"`
	LinkedID string `json:"linked_id" jsonschema:"linked entity id"`
	Rel      string `json:"rel" jsonschema:"issue, task, chat or workpackage"`
	Remove   bool   `json:"remove,omitempty" jsonschema:"remove instead of add"`
}

func registerWorkpackageTools(server *mcp.Server, dir string) {
	svc := func() *workpackageapp.Service { return workpackageapp.New(dir) }
	resolved := func(ctx context.Context, id string, fn func(*workpackageapp.Service, string) (map[string]any, error)) (*mcp.CallToolResult, map[string]any, error) {
		s := svc()
		full, err := s.ResolveID(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		data, err := fn(s, full)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			data = map[string]any{}
		}
		return nil, data, nil
	}

	mcp.AddTool(server, &mcp.Tool{Name: "wp_new", Description: "Create a workpackage"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpNewParams) (*mcp.CallToolResult, map[string]any, error) {
			id, err := svc().Create(ctx, workpackageapp.CreateInput{Title: a.Title, Body: a.Body, Kind: a.Kind, Priority: a.Priority, Parent: a.Parent, DependsOn: a.DependsOn})
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_edit", Description: "Edit a workpackage"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpEditParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Edit(ctx, id, workpackageapp.EditInput{Title: a.Title, Body: a.Body, Priority: a.Priority, Parent: a.Parent, ClearParent: a.ClearParent})
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_finding", Description: "Record a bug branch against a submission"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpFindingParams) (*mcp.CallToolResult, map[string]any, error) {
			id, err := svc().Finding(ctx, a.ParentID, a.SubmitID, workpackageapp.CreateInput{Title: a.Title, Body: a.Body, Parent: a.Parent})
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"id": id}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_list", Description: "List workpackages with derived status (paginated)"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpListParams) (*mcp.CallToolResult, map[string]any, error) {
			s := svc()
			filter, err := wpListFilter(ctx, s, dir, a.Ready, a.Mine, a.Blocked, a.InReview, a.Parent)
			if err != nil {
				return nil, nil, err
			}
			res, err := s.List(ctx, filter)
			if err != nil {
				return nil, nil, err
			}
			data := paginatedResult(res.Items, a.Cursor, a.Limit)
			data["history_complete"] = res.HistoryComplete
			data["observation_scope"] = "local_refs_only"
			return nil, data, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_show", Description: "Show a workpackage with derived status"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpIDParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				sh, err := s.Show(ctx, id)
				if err != nil {
					return nil, err
				}
				return sh.Data(), nil
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_start", Description: "Claim a workpackage (no fetch/sync over MCP)"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpStartParams) (*mcp.CallToolResult, map[string]any, error) {
			d, err := parseLease("claim", a.Lease, a.Lease != "")
			if err != nil {
				return nil, nil, err
			}
			s := svc()
			res, err := s.Start(ctx, a.ID, workpackageapp.StartOptions{Branch: a.Branch, Lease: d})
			if err != nil {
				return nil, nil, err
			}
			out, err := s.Outcome(ctx, res.ID, res.ClaimID, false)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"claim_id": out.ClaimID, "acquired": true, "published": false, "active_claim": out.ActiveClaim}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_renew", Description: "Extend your claim"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpRenewParams) (*mcp.CallToolResult, map[string]any, error) {
			d, err := parseLease("renew", a.Lease, a.Lease != "")
			if err != nil {
				return nil, nil, err
			}
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				cid, err := s.Renew(ctx, id, a.ClaimID, leaseOrDefault(d), a.Branch)
				return map[string]any{"claim_id": cid}, err
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_release", Description: "Release your claim"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpReleaseParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				cid, err := s.Release(ctx, id, a.ClaimID, a.Message)
				return map[string]any{"claim_id": cid}, err
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_submit", Description: "Submit a commit OID for acceptance"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpSubmitParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				sid, err := s.Submit(ctx, id, workpackageapp.SubmitInput{Commit: a.Commit, NoCommit: a.NoCommit, Branch: a.Branch, PRURL: a.PRURL, Reviewers: a.Reviewers, Note: a.Message})
				return map[string]any{"submit_id": sid}, err
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_accept", Description: "Accept the current submission"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpAcceptParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				aid, err := s.Accept(ctx, id, a.SubmitID, a.Message)
				return map[string]any{"accept_id": aid}, err
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_waive", Description: "Waive a finding"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpWaiveParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Waive(ctx, id, a.Target, a.Message)
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_cancel", Description: "Cancel a workpackage"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpMessageParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Cancel(ctx, id, a.Message)
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_dep", Description: "Add or remove an execution dependency"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpDepParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Dep(ctx, id, a.Target, a.Remove)
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_note", Description: "Add a note"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpMessageParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Note(ctx, id, a.Message)
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "wp_link", Description: "Link to an issue/task/chat/workpackage"},
		func(ctx context.Context, req *mcp.CallToolRequest, a wpLinkParams) (*mcp.CallToolResult, map[string]any, error) {
			return resolved(ctx, a.ID, func(s *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, s.Link(ctx, id, a.Rel, a.LinkedID, a.Remove)
			})
		})
}
