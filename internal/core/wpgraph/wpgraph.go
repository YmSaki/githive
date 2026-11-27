// Package wpgraph derives cross-ref workpackage state (derived_status,
// blocked_reasons, stale_approval, warnings, verified, incomplete_inputs)
// from the observed meta.json set. It is a pure function of its Input: no
// git I/O and no wall-clock reads (now is an input). Fold stays ref-local;
// everything that depends on other refs or on time lives here and is never
// stored in meta.json.
package wpgraph

import (
	"sort"
	"time"
)

// Derived statuses, in evaluation priority order.
const (
	StatusCancelled = "Cancelled"
	StatusCompleted = "Completed"
	StatusBlocked   = "Blocked"
	StatusInReview  = "InReview"
	StatusActive    = "Active"
	StatusReady     = "Ready"
	StatusPlanned   = "Planned"
)

// LedgerState is the users/registry classification (3 categories, with the
// incomplete/invalid split of category 2).
type LedgerState int

const (
	// LedgerNone means no users/registry and no trust root (unsigned mode).
	LedgerNone LedgerState = iota
	// LedgerComplete means a local, complete, healthy registry.
	LedgerComplete
	// LedgerIncomplete means shallow / unfetched / pinned-but-absent.
	LedgerIncomplete
	// LedgerInvalid means the complete registry history is corrupt.
	LedgerInvalid
)

// LedgerUser is one registry user as seen by approval checks.
type LedgerUser struct {
	Username string
	Active   bool
	Emails   []string
}

// Ledger is the registry snapshot handed to the graph.
type Ledger struct {
	State LedgerState
	Users []LedgerUser
}

// resolve maps an email (exact match) to a user; ok=false when the email is
// unknown or ambiguous.
func (l Ledger) resolve(email string) (LedgerUser, bool) {
	var found []LedgerUser
	for _, u := range l.Users {
		for _, e := range u.Emails {
			if e == email {
				found = append(found, u)
				break
			}
		}
	}
	if len(found) != 1 {
		return LedgerUser{}, false
	}
	return found[0], true
}

// Input is everything the derivation may depend on.
type Input struct {
	// WPs maps workpackage ID to its observed meta.json (decoded JSON).
	WPs map[string]map[string]any
	// Shallow holds workpackage IDs whose ref contains a shallow boundary.
	Shallow map[string]bool
	// Ledger is the users/registry snapshot and its classification.
	Ledger Ledger
	// RefVerified maps observed workpackage IDs to their signature/ledger
	// verification result. Normal reads pass nil (all false).
	RefVerified map[string]bool
	Now         time.Time
}

// BlockedReason is one entry of blocked_reasons[].
type BlockedReason struct {
	Type     string
	TargetID string
	// Accepted is set only for open_finding.
	Accepted *bool
}

// Map renders the reason in its JSON shape.
func (b BlockedReason) Map() map[string]any {
	m := map[string]any{"type": b.Type, "target_id": b.TargetID}
	if b.Accepted != nil {
		m["accepted"] = *b.Accepted
	}
	return m
}

// Result is the derived view of one workpackage.
type Result struct {
	ID                      string
	DerivedStatus           string
	BlockedReasons          []BlockedReason
	StaleApproval           bool
	Warnings                []string
	Verified                bool
	IncompleteInputs        []string
	ActiveClaim             map[string]any
	RegistryHistoryComplete bool
	RegistryValid           bool
}

// Fields renders the derived fields as the --json keys.
func (r Result) Fields() map[string]any {
	reasons := make([]any, len(r.BlockedReasons))
	for i, b := range r.BlockedReasons {
		reasons[i] = b.Map()
	}
	var claim any
	if r.ActiveClaim != nil {
		claim = r.ActiveClaim
	}
	return map[string]any{
		"active_claim":              claim,
		"blocked_reasons":           reasons,
		"derived_status":            r.DerivedStatus,
		"incomplete_inputs":         strs(r.IncompleteInputs),
		"observation_scope":         "local_refs_only",
		"registry_history_complete": r.RegistryHistoryComplete,
		"registry_valid":            r.RegistryValid,
		"stale_approval":            r.StaleApproval,
		"verified":                  r.Verified,
		"warnings":                  strs(r.Warnings),
	}
}

func strs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ---- meta accessors (decoded JSON) ----

func list(m map[string]any, key string) []map[string]any {
	raw, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

func strList(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func isCancelled(m map[string]any) bool { return len(list(m, "cancellations")) > 0 }

func isContainer(m map[string]any) bool { return str(m, "kind") == "container" }

func findingOf(m map[string]any) (parent, submit string, ok bool) {
	f, isObj := m["finding_of"].(map[string]any)
	if !isObj {
		return "", "", false
	}
	parent, submit = str(f, "workpackage"), str(f, "submit_id")
	return parent, submit, parent != "" && submit != ""
}

func submission(m map[string]any, id string) map[string]any {
	for _, s := range list(m, "submissions") {
		if str(s, "event_id") == id {
			return s
		}
	}
	return nil
}

// ---- derivation ----

type graph struct {
	in       Input
	ids      []string
	children map[string][]string // observed direct children by parent_id
	findings map[string][]string // recognized findings by parent P
	warn     map[string]map[string]bool
	// validAccept[w][submitID] = number of valid accepts.
	validAccept map[string]map[string]int
	// waived[P][F] = P holds a valid waiver for F.
	waived map[string]map[string]bool
}

func (g *graph) addWarn(id, code string) {
	if g.warn[id] == nil {
		g.warn[id] = map[string]bool{}
	}
	g.warn[id][code] = true
}

// Derive computes the Result of every observed workpackage.
func Derive(in Input) map[string]Result {
	g := &graph{
		in: in, children: map[string][]string{}, findings: map[string][]string{},
		warn: map[string]map[string]bool{}, validAccept: map[string]map[string]int{},
		waived: map[string]map[string]bool{},
	}
	for id := range in.WPs {
		g.ids = append(g.ids, id)
	}
	sort.Strings(g.ids)
	for _, id := range g.ids {
		m := in.WPs[id]
		if p := str(m, "parent_id"); p != "" {
			g.children[p] = append(g.children[p], id)
		}
		if parent, submit, ok := findingOf(m); ok {
			if pm, observed := in.WPs[parent]; observed {
				if submission(pm, submit) != nil {
					g.findings[parent] = append(g.findings[parent], id)
				} else {
					g.addWarn(id, "orphaned_finding")
				}
			}
		}
	}
	for _, id := range g.ids {
		g.judgeAccepts(id)
		g.judgeWaivers(id)
	}

	execCycle := g.sccMembers(g.execEdges)
	wbsCycle := g.sccMembers(func(u string) []string {
		if p := str(in.WPs[u], "parent_id"); p != "" {
			return []string{p}
		}
		return nil
	})
	inCycle := func(id string) bool { return execCycle[id] || wbsCycle[id] }

	done := g.fixpoint(inCycle)

	out := make(map[string]Result, len(g.ids))
	for _, id := range g.ids {
		out[id] = g.result(id, done, execCycle, wbsCycle)
	}
	return out
}

// judgeAccepts counts valid accepts per submission of w and records warnings.
func (g *graph) judgeAccepts(id string) {
	m := g.in.WPs[id]
	counts := map[string]int{}
	for _, acc := range list(m, "acceptances") {
		sub := submission(m, str(acc, "submit_id"))
		if sub == nil {
			continue
		}
		if g.qualified(id, str(acc, "by"), sub, "accept") {
			counts[str(sub, "event_id")]++
		}
	}
	g.validAccept[id] = counts
}

// qualified applies the shared accept/waive eligibility rules of §7.
func (g *graph) qualified(id, by string, sub map[string]any, op string) bool {
	reviewers := strList(sub["reviewers"])
	hasReviewers := sub["reviewers"] != nil
	switch g.in.Ledger.State {
	case LedgerIncomplete, LedgerInvalid:
		return false
	case LedgerNone:
		if by == str(sub, "by") {
			g.addWarn(id, "self_"+op)
		}
		// Unsigned project mode: the approval counts but is never verified.
		g.addWarn(id, "unverified_"+op)
		return true
	}
	unauthorized := "unauthorized_" + op
	submitter, ok1 := g.in.Ledger.resolve(str(sub, "by"))
	actor, ok2 := g.in.Ledger.resolve(by)
	if !ok1 || !ok2 || !submitter.Active || !actor.Active {
		g.addWarn(id, unauthorized)
		return false
	}
	if submitter.Username == actor.Username {
		g.addWarn(id, "self_"+op)
		return false
	}
	if hasReviewers {
		for _, r := range reviewers {
			if u, ok := g.in.Ledger.resolve(r); ok && u.Username == actor.Username {
				return true
			}
		}
		g.addWarn(id, unauthorized)
		return false
	}
	return true
}

func (g *graph) judgeWaivers(id string) {
	m := g.in.WPs[id]
	g.waived[id] = map[string]bool{}
	for _, w := range list(m, "waivers") {
		target := str(w, "target_id")
		tm, observed := g.in.WPs[target]
		if !observed {
			g.addWarn(id, "invalid_waive_target")
			continue
		}
		parent, submit, ok := findingOf(tm)
		if !ok || parent != id {
			g.addWarn(id, "invalid_waive_target")
			continue
		}
		orig := submission(m, submit)
		if orig == nil {
			g.addWarn(id, "invalid_waive_target")
			continue
		}
		if g.qualified(id, str(w, "by"), orig, "waive") {
			g.waived[id][target] = true
		}
	}
}

// execEdges are the G_exec successors of u (①〜③).
func (g *graph) execEdges(u string) []string {
	m := g.in.WPs[u]
	out := append([]string(nil), strList(m["depends_on"])...)
	if isContainer(m) {
		for _, c := range g.children[u] {
			if !isCancelled(g.in.WPs[c]) {
				out = append(out, c)
			}
		}
	}
	for _, f := range g.findings[u] {
		if !g.waived[u][f] && !isCancelled(g.in.WPs[f]) {
			out = append(out, f)
		}
	}
	return out
}

// sccMembers returns nodes in a non-trivial SCC (size >= 2 or a self edge)
// of the graph restricted to observed nodes.
func (g *graph) sccMembers(next func(string) []string) map[string]bool {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	counter := 0
	members := map[string]bool{}
	var strong func(v string)
	strong = func(v string) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range next(v) {
			if _, observed := g.in.WPs[w]; !observed {
				continue
			}
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			selfEdge := false
			for _, w := range next(v) {
				if w == v {
					selfEdge = true
				}
			}
			if len(comp) >= 2 || selfEdge {
				for _, w := range comp {
					members[w] = true
				}
			}
		}
	}
	for _, id := range g.ids {
		if _, seen := index[id]; !seen {
			strong(id)
		}
	}
	return members
}

func (g *graph) currentAccepted(id string) bool {
	m := g.in.WPs[id]
	cur := str(m, "current_submission_id")
	if cur == "" {
		return false
	}
	sub := submission(m, cur)
	return sub != nil && str(sub, "acceptance_version") == str(m, "acceptance_version") && g.validAccept[id][cur] > 0
}

// openFindings lists recognized findings of p that remain unresolved
// against the completion set s.
func (g *graph) openFindings(p string, s map[string]bool) []string {
	cur := str(g.in.WPs[p], "current_submission_id")
	var out []string
	for _, f := range g.findings[p] {
		if g.waived[p][f] || isCancelled(g.in.WPs[f]) {
			continue
		}
		_, submit, _ := findingOf(g.in.WPs[f])
		if submit == cur || !s[f] {
			out = append(out, f)
		}
	}
	return out
}

// fixpoint computes the least fixed point S*.
func (g *graph) fixpoint(inCycle func(string) bool) map[string]bool {
	s := map[string]bool{}
	for {
		next := map[string]bool{}
		for _, id := range g.ids {
			m := g.in.WPs[id]
			if isCancelled(m) || inCycle(id) {
				continue
			}
			depsDone := true
			for _, d := range strList(m["depends_on"]) {
				if !s[d] {
					depsDone = false
				}
			}
			if !depsDone || len(g.openFindings(id, s)) > 0 {
				continue
			}
			if isContainer(m) {
				kids := g.children[id]
				anyDone, allOK := false, len(kids) > 0
				for _, c := range kids {
					switch {
					case s[c]:
						anyDone = true
					case isCancelled(g.in.WPs[c]):
					default:
						allOK = false
					}
				}
				if allOK && anyDone {
					next[id] = true
				}
				continue
			}
			if g.currentAccepted(id) {
				next[id] = true
			}
		}
		if len(next) == len(s) {
			return next
		}
		s = next
	}
}

func (g *graph) activeClaim(m map[string]any) map[string]any { return ActiveClaim(m, g.in.Now) }

// ActiveClaim returns the winning live claim of meta at now: unreleased,
// now <= lease_until, smallest claim_id. Nil when there is none.
func ActiveClaim(m map[string]any, now time.Time) map[string]any {
	var best map[string]any
	for _, c := range list(m, "claims") {
		if c["released_at"] != nil {
			continue
		}
		until, err := time.Parse(time.RFC3339Nano, str(c, "lease_until"))
		if err != nil || now.After(until) {
			continue
		}
		if best == nil || str(c, "claim_id") < str(best, "claim_id") {
			best = c
		}
	}
	return best
}

func (g *graph) result(id string, done, execCycle, wbsCycle map[string]bool) Result {
	in := g.in
	m := in.WPs[id]
	r := Result{ID: id, ActiveClaim: g.activeClaim(m)}

	switch in.Ledger.State {
	case LedgerIncomplete:
		g.addWarn(id, "registry_incomplete")
	case LedgerInvalid:
		g.addWarn(id, "registry_invalid")
		r.RegistryHistoryComplete = true
	default:
		r.RegistryHistoryComplete, r.RegistryValid = true, true
	}

	cur := str(m, "current_submission_id")
	if cur != "" {
		curValid := g.validAccept[id][cur]
		pastValid := false
		for sub, n := range g.validAccept[id] {
			if sub != cur && n > 0 {
				pastValid = true
			}
		}
		curSub := submission(m, cur)
		versionMismatch := curSub != nil && str(curSub, "acceptance_version") != str(m, "acceptance_version")
		r.StaleApproval = (curValid == 0 && pastValid) || (versionMismatch && curValid > 0)
	}

	switch {
	case isCancelled(m):
		r.DerivedStatus = StatusCancelled
	case done[id]:
		r.DerivedStatus = StatusCompleted
	default:
		r.BlockedReasons = g.blockedReasons(id, done, execCycle, wbsCycle)
		switch {
		case len(r.BlockedReasons) > 0:
			r.DerivedStatus = StatusBlocked
		case !isContainer(m) && cur != "":
			r.DerivedStatus = StatusInReview
		case !isContainer(m) && r.ActiveClaim != nil:
			r.DerivedStatus = StatusActive
		case isContainer(m) && g.hasLiveChild(id):
			r.DerivedStatus = StatusActive
		case isContainer(m):
			r.DerivedStatus = StatusPlanned
		default:
			r.DerivedStatus = StatusReady
		}
	}
	if r.BlockedReasons == nil {
		r.BlockedReasons = []BlockedReason{}
	}

	r.IncompleteInputs, r.Verified = g.inspect(id)
	for code := range g.warn[id] {
		r.Warnings = append(r.Warnings, code)
	}
	sort.Strings(r.Warnings)
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	return r
}

func (g *graph) hasLiveChild(id string) bool {
	for _, c := range g.children[id] {
		if !isCancelled(g.in.WPs[c]) {
			return true
		}
	}
	return false
}

func (g *graph) blockedReasons(id string, done, execCycle, wbsCycle map[string]bool) []BlockedReason {
	m := g.in.WPs[id]
	var out []BlockedReason
	for _, d := range strList(m["depends_on"]) {
		dm, observed := g.in.WPs[d]
		switch {
		case !observed:
			out = append(out, BlockedReason{Type: "missing_dependency", TargetID: d})
		case isCancelled(dm):
			out = append(out, BlockedReason{Type: "cancelled_dependency", TargetID: d})
		case !done[d]:
			out = append(out, BlockedReason{Type: "unresolved_dependency", TargetID: d})
		}
	}
	if execCycle[id] {
		out = append(out, BlockedReason{Type: "cycle", TargetID: id})
	}
	if wbsCycle[id] {
		out = append(out, BlockedReason{Type: "wbs_cycle", TargetID: id})
	}
	accepted := g.currentAccepted(id)
	for _, f := range g.openFindings(id, done) {
		a := accepted
		out = append(out, BlockedReason{Type: "open_finding", TargetID: f, Accepted: &a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].TargetID < out[j].TargetID
	})
	dedup := out[:0]
	for i, b := range out {
		if i > 0 && b.Type == out[i-1].Type && b.TargetID == out[i-1].TargetID {
			continue
		}
		dedup = append(dedup, b)
	}
	return dedup
}

// inspect walks R_inspected(w) and returns incomplete_inputs and verified.
func (g *graph) inspect(id string) ([]string, bool) {
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		m, observed := g.in.WPs[u]
		if !observed {
			continue
		}
		var next []string
		next = append(next, strList(m["depends_on"])...)
		if isContainer(m) {
			next = append(next, g.children[u]...)
		}
		next = append(next, g.findings[u]...)
		if p := str(m, "parent_id"); p != "" {
			next = append(next, p)
		}
		if p, _, ok := findingOf(m); ok {
			next = append(next, p)
		}
		for _, w := range list(m, "waivers") {
			if t := str(w, "target_id"); t != "" {
				next = append(next, t)
			}
		}
		for _, v := range next {
			if !seen[v] {
				seen[v] = true
				queue = append(queue, v)
			}
		}
	}
	verified := g.in.Ledger.State == LedgerComplete
	var incomplete []string
	for v := range seen {
		_, observed := g.in.WPs[v]
		if !observed || g.in.Shallow[v] {
			incomplete = append(incomplete, "workpackage:"+v)
			verified = false
		} else if !g.in.RefVerified[v] {
			verified = false
		}
	}
	sort.Strings(incomplete)
	if incomplete == nil {
		incomplete = []string{}
	}
	return incomplete, verified
}
