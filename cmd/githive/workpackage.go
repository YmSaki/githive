package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ymsaki/githive/internal/app/idresolve"
	"github.com/ymsaki/githive/internal/app/workpackageapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
)

func newWorkpackageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workpackage", Aliases: []string{"wp"}, Short: "Manage workpackages (WBS, dependencies, claims, acceptance)"}
	cmd.AddCommand(
		newWPNewCmd(), newWPEditCmd(), newWPFindingCmd(), newWPListCmd(), newWPShowCmd(),
		newWPStartCmd(), newWPRenewCmd(), newWPReleaseCmd(), newWPSubmitCmd(), newWPAcceptCmd(),
		newWPWaiveCmd(), newWPCancelCmd(), newWPDepCmd(), newWPNoteCmd(), newWPLinkCmd(),
	)
	return cmd
}

func wpRef(id string) string { return workpackageapp.RefFor(id).String() }

// warnedError keeps an error's own code/exit/data and adds machine-readable
// warnings (prefetch_failed, sync_failed) under error.data.warnings.
type warnedError struct {
	cause    error
	warnings []cliout.Warning
}

func (e *warnedError) Error() string { return e.cause.Error() }
func (e *warnedError) Unwrap() error { return e.cause }

func withErrorWarnings(err error, warnings []cliout.Warning) error {
	if err == nil || len(warnings) == 0 {
		return err
	}
	return &warnedError{cause: err, warnings: warnings}
}

// classifyWorkpackageError maps workpackageapp's typed errors; ok is false
// for anything else.
func classifyWorkpackageError(err error) (int, cliout.ErrorInfo, bool) {
	var conflict *workpackageapp.ConflictError
	if errors.As(err, &conflict) {
		data := map[string]any{}
		for k, v := range conflict.Data {
			data[k] = v
		}
		return cliout.ExitSyncRetryExhausted, cliout.ErrorInfo{Code: conflict.Code, Message: err.Error(), Data: data}, true
	}
	var registry *workpackageapp.RegistryInvalidError
	if errors.As(err, &registry) {
		return cliout.ExitVerifyFailed, cliout.ErrorInfo{Code: "verify_failed", Message: err.Error(),
			Data: map[string]any{"reason": registry.Reason, "ref": "refs/projects/users/registry"}}, true
	}
	if errors.Is(err, workpackageapp.ErrNotFound) {
		return cliout.ExitGeneralError, cliout.ErrorInfo{Code: "not_found", Message: err.Error()}, true
	}
	return 0, cliout.ErrorInfo{}, false
}

func wpService() (*workpackageapp.Service, string, error) {
	dir, err := repoDir()
	if err != nil {
		return nil, "", err
	}
	return workpackageapp.New(dir), dir, nil
}

// wpWrite runs a resolved-id write, then the usual auto sync.
func wpWrite(idArg string, write func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error)) error {
	svc, dir, err := wpService()
	if err != nil {
		return err
	}
	ctx := context.Background()
	id, err := svc.ResolveID(ctx, idArg)
	if err != nil {
		return err
	}
	data, err := write(ctx, svc, id)
	if err != nil {
		return err
	}
	return wpFinish(dir, id, data)
}

func wpFinish(dir, id string, data map[string]any) error {
	warnings, err := syncIfEnabled(dir, wpRef(id))
	if err != nil {
		return err
	}
	if flags.json {
		return cliout.PrintSuccess(data, warnings)
	}
	if v, ok := data["id"]; ok {
		fmt.Println(v)
	}
	printWarnings(warnings)
	return nil
}

func optString(cmd *cobra.Command, name, value string) *string {
	if cmd.Flags().Changed(name) {
		return &value
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newWPNewCmd() *cobra.Command {
	var title, body, kind, priority, parent, depends string
	cmd := &cobra.Command{
		Use: "new", Short: "Create a workpackage",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, dir, err := wpService()
			if err != nil {
				return err
			}
			id, err := svc.Create(context.Background(), workpackageapp.CreateInput{
				Title: title, Body: optString(cmd, "body", body), Kind: kind, Priority: priority,
				Parent: parent, DependsOn: splitList(depends),
			})
			if err != nil {
				return err
			}
			return wpFinish(dir, id, map[string]any{"id": id})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "title (required)")
	cmd.Flags().StringVar(&body, "body", "", "acceptance criteria / body")
	cmd.Flags().StringVar(&kind, "kind", "", "work or container (default work)")
	cmd.Flags().StringVar(&priority, "priority", "", "low, normal or high (default normal)")
	cmd.Flags().StringVar(&parent, "parent", "", "WBS parent workpackage id")
	cmd.Flags().StringVar(&depends, "depends-on", "", "comma separated workpackage ids this one depends on")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newWPEditCmd() *cobra.Command {
	var title, body, priority, parent string
	var clearParent bool
	cmd := &cobra.Command{
		Use: "edit <id>", Short: "Edit a workpackage", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Edit(ctx, id, workpackageapp.EditInput{
					Title: optString(cmd, "title", title), Body: optString(cmd, "body", body),
					Priority: optString(cmd, "priority", priority), Parent: optString(cmd, "parent", parent), ClearParent: clearParent,
				})
			})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&body, "body", "", "new body (changes acceptance_version)")
	cmd.Flags().StringVar(&priority, "priority", "", "low, normal or high")
	cmd.Flags().StringVar(&parent, "parent", "", "new WBS parent id")
	cmd.Flags().BoolVar(&clearParent, "clear-parent", false, "remove the WBS parent")
	return cmd
}

func newWPFindingCmd() *cobra.Command {
	var title, body, submitID, parent string
	cmd := &cobra.Command{
		Use: "finding <parent-id>", Short: "Record a bug branch against a submission", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, dir, err := wpService()
			if err != nil {
				return err
			}
			id, err := svc.Finding(context.Background(), args[0], submitID, workpackageapp.CreateInput{
				Title: title, Body: optString(cmd, "body", body), Parent: parent,
			})
			if err != nil {
				return err
			}
			return wpFinish(dir, id, map[string]any{"id": id})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "title (required)")
	cmd.Flags().StringVar(&body, "body", "", "body")
	cmd.Flags().StringVar(&submitID, "submit-id", "", "submission the finding is about (default: current)")
	cmd.Flags().StringVar(&parent, "parent", "", "WBS parent id (default: none)")
	cmd.MarkFlagRequired("title")
	return cmd
}

func wpListFilter(ctx context.Context, svc *workpackageapp.Service, dir string, ready, mine, blocked, inReview bool, parent string) (workpackageapp.ListFilter, error) {
	filter := workpackageapp.ListFilter{Ready: ready, Mine: mine, Blocked: blocked, InReview: inReview}
	if mine {
		sig, err := identity.Resolve(ctx, dir)
		if err != nil {
			return filter, err
		}
		filter.Self = sig.Email
	}
	if parent != "" {
		resolved, err := svc.ResolveParentFilter(ctx, parent)
		if err != nil {
			return filter, err
		}
		filter.Parent = resolved
	}
	return filter, nil
}

func wpListData(res *workpackageapp.ListResult) map[string]any {
	data := cliout.ItemsMap(res.Items, len(res.Items))
	data["history_complete"] = res.HistoryComplete
	data["observation_scope"] = "local_refs_only"
	return data
}

func newWPListCmd() *cobra.Command {
	var ready, mine, blocked, inReview bool
	var parent string
	cmd := &cobra.Command{
		Use: "list", Short: "List workpackages with derived status",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, dir, err := wpService()
			if err != nil {
				return err
			}
			ctx := context.Background()
			filter, err := wpListFilter(ctx, svc, dir, ready, mine, blocked, inReview, parent)
			if err != nil {
				return err
			}
			res, err := svc.List(ctx, filter)
			if err != nil {
				return err
			}
			if flags.json {
				return cliout.PrintSuccess(wpListData(res), nil)
			}
			displayIDs, err := idresolve.DisplayIDs(ctx, dir, "workpackage")
			if err != nil {
				return err
			}
			width := displayIDWidth(displayIDs, res.Items)
			fmt.Printf("%-*s %-10s %-9s %s\n", width, "ID", "STATUS", "KIND", "TITLE")
			for _, m := range res.Items {
				fmt.Printf("%-*s %-10v %-9v %v%s\n", width, displayID(displayIDs, m["id"]), m["derived_status"], m["kind"], m["title"], wpWarningSuffix(m["warnings"]))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&ready, "ready", false, "only Ready workpackages")
	cmd.Flags().BoolVar(&mine, "mine", false, "only workpackages whose winning claim is mine")
	cmd.Flags().BoolVar(&blocked, "blocked", false, "only Blocked workpackages")
	cmd.Flags().BoolVar(&inReview, "in-review", false, "only InReview workpackages")
	cmd.Flags().StringVar(&parent, "parent", "", "only children of this WBS parent")
	return cmd
}

func newWPShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <id>", Short: "Show a workpackage", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := wpService()
			if err != nil {
				return err
			}
			ctx := context.Background()
			id, err := svc.ResolveID(ctx, args[0])
			if err != nil {
				return err
			}
			sh, err := svc.Show(ctx, id)
			if err != nil {
				return err
			}
			if flags.json {
				return cliout.PrintSuccess(sh.Data(), nil)
			}
			fmt.Printf("%v  %v  %v  %v\n", sh.Meta["id"], sh.Derived["derived_status"], sh.Meta["kind"], sh.Meta["title"])
			for _, code := range wpWarningCodes(sh.Derived["warnings"]) {
				fmt.Printf("warning: %s\n", code)
			}
			if sh.ReadSource == workpackageapp.SourceSnapshot {
				fmt.Println("(snapshot: history is shallow)")
			}
			if sh.Body != "" {
				fmt.Println()
				fmt.Println(sh.Body)
			}
			for _, n := range sh.Notes {
				fmt.Printf("\n--- %v %v %v (%v) ---\n%v\n", n["event_id"], n["kind"], n["actor"], n["ts"], n["body"])
			}
			return nil
		},
	}
}

// wpWarningCodes returns a read view's derived approval warning codes
// (self_accept, unverified_accept, self_waive, unverified_waive, ...).
func wpWarningCodes(v any) []string {
	raw, _ := v.([]any)
	codes := make([]string, 0, len(raw))
	for _, c := range raw {
		if s, ok := c.(string); ok {
			codes = append(codes, s)
		}
	}
	return codes
}

// wpWarningSuffix renders a list row's warnings as " [a b]", or "".
func wpWarningSuffix(v any) string {
	if codes := wpWarningCodes(v); len(codes) > 0 {
		return " [" + strings.Join(codes, " ") + "]"
	}
	return ""
}

// parseLease converts a --lease / lease argument. given is false when the
// caller omitted it, which yields nil (the app applies DefaultLease); an
// explicit value is parsed and checked against workpackageapp.ValidateLease
// here, so "0s" or "1ns" is invalid_input instead of a silent default or an
// already-expired claim.
func parseLease(op, s string, given bool) (*time.Duration, error) {
	if !given {
		return nil, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, wpInvalid("lease", "must be a Go duration such as 24h")
	}
	if err := workpackageapp.ValidateLease(op, d); err != nil {
		return nil, err
	}
	return &d, nil
}

// leaseOrDefault resolves an omitted renew lease to DefaultLease.
func leaseOrDefault(d *time.Duration) time.Duration {
	if d == nil {
		return workpackageapp.DefaultLease
	}
	return *d
}

func newWPStartCmd() *cobra.Command {
	var branch, lease string
	cmd := &cobra.Command{
		Use: "start <id>", Short: "Claim a workpackage (advisory lease)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, dir, err := wpService()
			if err != nil {
				return err
			}
			d, err := parseLease("claim", lease, cmd.Flags().Changed("lease"))
			if err != nil {
				return err
			}
			ctx := context.Background()
			res, err := svc.Start(ctx, args[0], workpackageapp.StartOptions{Branch: branch, Lease: d, Fetch: !flags.noSync, Remote: flags.remote})
			var warnings []cliout.Warning
			if res != nil && res.PrefetchErr != nil {
				warnings = append(warnings, cliout.Warning{Code: "prefetch_failed", Message: res.PrefetchErr.Error(), Data: map[string]any{"remote": flags.remote}})
			}
			if err != nil {
				return withErrorWarnings(err, warnings)
			}
			warnings, err = syncIfEnabled(dir, wpRef(res.ID), warnings)
			if err != nil {
				return withErrorWarnings(err, warnings)
			}
			published := !flags.noSync
			for _, w := range warnings {
				if w.Code == "sync_failed" {
					published, _ = w.Data["published"].(bool)
				}
			}
			out, err := svc.Outcome(ctx, res.ID, res.ClaimID, published)
			if err != nil {
				return withErrorWarnings(err, warnings)
			}
			data := map[string]any{"claim_id": out.ClaimID, "acquired": true, "published": published, "active_claim": out.ActiveClaim}
			if flags.json {
				return cliout.PrintSuccess(data, warnings)
			}
			fmt.Println(out.ClaimID)
			printWarnings(warnings)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "branch hint (no checkout is performed)")
	cmd.Flags().StringVar(&lease, "lease", "", "lease duration, at least 1ms (default 24h, at most 168h)")
	return cmd
}

func newWPRenewCmd() *cobra.Command {
	var claimID, lease, branch string
	cmd := &cobra.Command{
		Use: "renew <id>", Short: "Extend your claim", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := parseLease("renew", lease, cmd.Flags().Changed("lease"))
			if err != nil {
				return err
			}
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				cid, err := svc.Renew(ctx, id, claimID, leaseOrDefault(d), branch)
				return map[string]any{"claim_id": cid}, err
			})
		},
	}
	cmd.Flags().StringVar(&claimID, "claim-id", "", "claim to renew (default: your only live claim)")
	cmd.Flags().StringVar(&lease, "lease", "", "new lease duration from now (default 24h)")
	cmd.Flags().StringVar(&branch, "branch", "", "new branch hint")
	return cmd
}

func newWPReleaseCmd() *cobra.Command {
	var claimID, note string
	cmd := &cobra.Command{
		Use: "release <id>", Short: "Release your claim", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				cid, err := svc.Release(ctx, id, claimID, note)
				return map[string]any{"claim_id": cid}, err
			})
		},
	}
	cmd.Flags().StringVar(&claimID, "claim-id", "", "claim to release (default: your only unreleased claim)")
	cmd.Flags().StringVarP(&note, "message", "m", "", "note")
	return cmd
}

func newWPSubmitCmd() *cobra.Command {
	var commit, branch, prURL, reviewers, note string
	var noCommit bool
	cmd := &cobra.Command{
		Use: "submit <id>", Short: "Submit a commit OID for acceptance", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				in := workpackageapp.SubmitInput{Commit: optString(cmd, "commit", commit), NoCommit: noCommit, Branch: branch, PRURL: prURL, Note: note}
				if cmd.Flags().Changed("reviewers") {
					in.Reviewers = splitList(reviewers)
					if in.Reviewers == nil {
						in.Reviewers = []string{}
					}
				}
				sid, err := svc.Submit(ctx, id, in)
				return map[string]any{"submit_id": sid}, err
			})
		},
	}
	cmd.Flags().StringVar(&commit, "commit", "", "full commit OID (default: HEAD)")
	cmd.Flags().BoolVar(&noCommit, "no-commit", false, "submit without an artifact commit")
	cmd.Flags().StringVar(&branch, "branch", "", "branch hint")
	cmd.Flags().StringVar(&prURL, "pr-url", "", "pull request URL (reference only)")
	cmd.Flags().StringVar(&reviewers, "reviewers", "", "comma separated usernames or emails")
	cmd.Flags().StringVarP(&note, "message", "m", "", "note")
	return cmd
}

func newWPAcceptCmd() *cobra.Command {
	var submitID, note string
	cmd := &cobra.Command{
		Use: "accept <id>", Short: "Accept the current submission", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				aid, err := svc.Accept(ctx, id, submitID, note)
				return map[string]any{"accept_id": aid}, err
			})
		},
	}
	cmd.Flags().StringVar(&submitID, "submit-id", "", "submission to accept (default: current)")
	cmd.Flags().StringVarP(&note, "message", "m", "", "note")
	return cmd
}

func newWPWaiveCmd() *cobra.Command {
	var target, reason string
	cmd := &cobra.Command{
		Use: "waive <id>", Short: "Waive a finding of this workpackage", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Waive(ctx, id, target, reason)
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "finding workpackage id (required)")
	cmd.Flags().StringVarP(&reason, "message", "m", "", "reason (required)")
	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("message")
	return cmd
}

func newWPCancelCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use: "cancel <id>", Short: "Cancel a workpackage", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Cancel(ctx, id, reason)
			})
		},
	}
	cmd.Flags().StringVarP(&reason, "message", "m", "", "reason")
	return cmd
}

func newWPDepCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use: "dep <id> <target-id>", Short: "Add or remove an execution dependency", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Dep(ctx, id, args[1], remove)
			})
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the dependency")
	return cmd
}

func newWPNoteCmd() *cobra.Command {
	var message, file string
	cmd := &cobra.Command{
		Use: "note <id>", Short: "Add a note", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				content, err := readFile(file)
				if err != nil {
					return err
				}
				message = content
			}
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Note(ctx, id, message)
			})
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "note body")
	cmd.Flags().StringVarP(&file, "file", "F", "", "read note body from file")
	return cmd
}

func newWPLinkCmd() *cobra.Command {
	var rel string
	var remove bool
	cmd := &cobra.Command{
		Use: "link <id> <linked-id>", Short: "Link to an issue/task/chat/workpackage", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return wpWrite(args[0], func(ctx context.Context, svc *workpackageapp.Service, id string) (map[string]any, error) {
				return nil, svc.Link(ctx, id, rel, args[1], remove)
			})
		},
	}
	cmd.Flags().StringVar(&rel, "rel", "", "issue, task, chat or workpackage (required)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the link")
	cmd.MarkFlagRequired("rel")
	return cmd
}

func wpInvalid(field, rule string) error {
	return &event.InvalidInputError{Kind: "workpackage", Field: field, Rule: rule}
}
