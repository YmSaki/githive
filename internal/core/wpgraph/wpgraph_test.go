package wpgraph

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

func id(n int) string { return fmt.Sprintf("01jzc0wpaaaaaaaaaaaaa%05d", n) }

// wp builds an observed meta.json with every field present.
func wp(n int, kind string) map[string]any {
	return map[string]any{
		"acceptance_version": id(n), "acceptances": []any{}, "cancellations": []any{}, "claims": []any{},
		"created_at": "2026-07-04T10:00:00.000Z", "created_by": "dev@example.org", "current_submission_id": nil,
		"depends_on": []any{}, "finding_of": nil, "id": id(n), "kind": kind, "links": []any{}, "parent_id": nil,
		"priority": "normal", "submissions": []any{}, "title": "t", "updated_at": "2026-07-04T10:00:00.000Z", "waivers": []any{},
	}
}

func submit(m map[string]any, sid int, by string, reviewers ...string) {
	var rv any
	if len(reviewers) > 0 {
		arr := []any{}
		for _, r := range reviewers {
			arr = append(arr, r)
		}
		rv = arr
	}
	m["submissions"] = append(m["submissions"].([]any), map[string]any{
		"acceptance_version": m["acceptance_version"], "branch": nil, "by": by, "commit": nil,
		"event_id": id(sid), "pr_url": nil, "reviewers": rv, "ts": "2026-07-04T10:00:00.000Z",
	})
	m["current_submission_id"] = id(sid)
}

func accept(m map[string]any, aid, sid int, by string) {
	m["acceptances"] = append(m["acceptances"].([]any), map[string]any{"by": by, "event_id": id(aid), "submit_id": id(sid), "ts": "2026-07-04T10:00:00.000Z"})
}

func waive(m map[string]any, wid, target int, by string) {
	m["waivers"] = append(m["waivers"].([]any), map[string]any{"by": by, "event_id": id(wid), "reason": "r", "target_id": id(target), "ts": "2026-07-04T10:00:00.000Z"})
}

func cancel(m map[string]any) {
	m["cancellations"] = []any{map[string]any{"by": "x@example.org", "event_id": id(999), "ts": "2026-07-04T10:00:00.000Z"}}
}

func deps(m map[string]any, ns ...int) {
	arr := []any{}
	for _, n := range ns {
		arr = append(arr, id(n))
	}
	m["depends_on"] = arr
}

func finding(n, parent, sid int) map[string]any {
	m := wp(n, "work")
	m["finding_of"] = map[string]any{"workpackage": id(parent), "submit_id": id(sid)}
	return m
}

// done makes a work package complete: submitted by dev, accepted by rev.
func done(m map[string]any, base int) map[string]any {
	submit(m, base, "dev@example.org")
	accept(m, base+1, base, "rev@example.org")
	return m
}

func derive(wps ...map[string]any) map[string]Result {
	return deriveWith(Ledger{State: LedgerNone}, nil, wps...)
}

func deriveWith(ledger Ledger, shallow map[string]bool, wps ...map[string]any) map[string]Result {
	in := Input{WPs: map[string]map[string]any{}, Shallow: shallow, Ledger: ledger, Now: now}
	for _, m := range wps {
		in.WPs[m["id"].(string)] = m
	}
	return Derive(in)
}

func reasons(r Result) []string {
	var out []string
	for _, b := range r.BlockedReasons {
		out = append(out, b.Type+":"+b.TargetID)
	}
	return out
}

func TestStatusPriority(t *testing.T) {
	ready := wp(1, "work")
	claimed := wp(2, "work")
	claimed["claims"] = []any{map[string]any{"branch": nil, "claim_id": id(20), "holder": "dev@example.org", "lease_until": "2026-07-05T00:00:00.000Z", "released_at": nil, "ts": "x"}}
	expired := wp(3, "work")
	expired["claims"] = []any{map[string]any{"branch": nil, "claim_id": id(30), "holder": "dev@example.org", "lease_until": "2026-07-04T11:00:00.000Z", "released_at": nil, "ts": "x"}}
	review := wp(4, "work")
	submit(review, 40, "dev@example.org")
	completed := done(wp(5, "work"), 50)
	cancelled := done(wp(6, "work"), 60)
	cancel(cancelled)
	planned := wp(7, "container")
	res := derive(ready, claimed, expired, review, completed, cancelled, planned)
	want := map[int]string{1: StatusReady, 2: StatusActive, 3: StatusReady, 4: StatusInReview, 5: StatusCompleted, 6: StatusCancelled, 7: StatusPlanned}
	for n, s := range want {
		if got := res[id(n)].DerivedStatus; got != s {
			t.Errorf("wp %d: %s, want %s", n, got, s)
		}
	}
	if res[id(2)].ActiveClaim["claim_id"] != id(20) || res[id(3)].ActiveClaim != nil {
		t.Fatal("active claim selection")
	}
}

func TestDependencyReasons(t *testing.T) {
	a := done(wp(1, "work"), 10)
	deps(a, 2, 3, 4)
	b := wp(2, "work")
	c := wp(3, "work")
	cancel(c)
	res := derive(a, b, c)
	got := reasons(res[id(1)])
	want := []string{"cancelled_dependency:" + id(3), "missing_dependency:" + id(4), "unresolved_dependency:" + id(2)}
	if !reflect.DeepEqual(got, want) || res[id(1)].DerivedStatus != StatusBlocked {
		t.Fatalf("got %v %s", got, res[id(1)].DerivedStatus)
	}
	// Completing the dependency completes the dependent.
	done(b, 20)
	deps(a, 2)
	if res := derive(a, b); res[id(1)].DerivedStatus != StatusCompleted {
		t.Fatalf("got %s", res[id(1)].DerivedStatus)
	}
}

// §11-1: container/child mutual cycle and work-work WBS cycle never
// complete, and their dependents stay incomplete too.
func TestCyclesConvergeIncomplete(t *testing.T) {
	cont := wp(1, "container")
	child := done(wp(2, "work"), 20)
	child["parent_id"] = id(1)
	deps(child, 1) // child depends on its container: exec cycle 1->2->1
	dependent := done(wp(3, "work"), 30)
	deps(dependent, 1)
	w1 := done(wp(4, "work"), 40)
	w2 := done(wp(5, "work"), 50)
	w1["parent_id"], w2["parent_id"] = id(5), id(4)
	res := derive(cont, child, dependent, w1, w2)
	for _, n := range []int{1, 2} {
		if r := res[id(n)]; r.DerivedStatus != StatusBlocked || !contains(reasons(r), "cycle:"+id(n)) {
			t.Errorf("wp %d: %s %v", n, r.DerivedStatus, reasons(r))
		}
	}
	if r := res[id(3)]; r.DerivedStatus != StatusBlocked || !contains(reasons(r), "unresolved_dependency:"+id(1)) {
		t.Errorf("dependent: %s %v", r.DerivedStatus, reasons(r))
	}
	for _, n := range []int{4, 5} {
		if r := res[id(n)]; r.DerivedStatus != StatusBlocked || !contains(reasons(r), "wbs_cycle:"+id(n)) {
			t.Errorf("wbs %d: %s %v", n, r.DerivedStatus, reasons(r))
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// §11-2: a body edit after acceptance makes the approval stale.
func TestStaleApprovalAfterBodyEdit(t *testing.T) {
	a := done(wp(1, "work"), 10)
	a["acceptance_version"] = id(99)
	r := derive(a)[id(1)]
	if r.DerivedStatus != StatusInReview || !r.StaleApproval {
		t.Fatalf("got %s stale=%v", r.DerivedStatus, r.StaleApproval)
	}
	// Resubmission without a new accept: still stale (past approval only).
	submit(a, 12, "dev@example.org")
	r = derive(a)[id(1)]
	if r.DerivedStatus != StatusInReview || !r.StaleApproval {
		t.Fatalf("resubmitted: %s stale=%v", r.DerivedStatus, r.StaleApproval)
	}
	accept(a, 13, 12, "rev@example.org")
	if r := derive(a)[id(1)]; r.DerivedStatus != StatusCompleted || r.StaleApproval {
		t.Fatalf("reaccepted: %s stale=%v", r.DerivedStatus, r.StaleApproval)
	}
}

func ledger(users ...LedgerUser) Ledger { return Ledger{State: LedgerComplete, Users: users} }

// §11-3: case-sensitive emails, aliases, incomplete/invalid/none ledgers.
func TestApprovalEligibility(t *testing.T) {
	dev := LedgerUser{Username: "dev", Active: true, Emails: []string{"dev@example.org"}}
	devUpper := LedgerUser{Username: "devupper", Active: true, Emails: []string{"Dev@example.org"}}
	alias := LedgerUser{Username: "multi", Active: true, Emails: []string{"a@x", "b@x"}}
	inactive := LedgerUser{Username: "gone", Active: false, Emails: []string{"gone@x"}}

	mk := func(by, acceptor string, reviewers ...string) map[string]any {
		m := wp(1, "work")
		submit(m, 10, by, reviewers...)
		accept(m, 11, 10, acceptor)
		return m
	}
	cases := []struct {
		name   string
		ledger Ledger
		wp     map[string]any
		status string
		warn   []string
	}{
		{"case differs: only the named reviewer counts", ledger(dev, devUpper, alias), mk("a@x", "Dev@example.org", "Dev@example.org"), StatusCompleted, []string{}},
		{"case differs: other case is unauthorized", ledger(dev, devUpper, alias), mk("a@x", "dev@example.org", "Dev@example.org"), StatusInReview, []string{"unauthorized_accept"}},
		{"alias of the submitter is self accept", ledger(dev, alias), mk("a@x", "b@x"), StatusInReview, []string{"self_accept"}},
		{"reviewer alias resolves to the same user", ledger(dev, alias), mk("dev@example.org", "b@x", "a@x"), StatusCompleted, []string{}},
		{"inactive acceptor", ledger(dev, inactive), mk("dev@example.org", "gone@x"), StatusInReview, []string{"unauthorized_accept"}},
		{"unregistered acceptor", ledger(dev), mk("dev@example.org", "who@x"), StatusInReview, []string{"unauthorized_accept"}},
		{"incomplete ledger grants nothing", Ledger{State: LedgerIncomplete}, mk("dev@example.org", "rev@x"), StatusInReview, []string{"registry_incomplete"}},
		{"invalid ledger grants nothing", Ledger{State: LedgerInvalid}, mk("dev@example.org", "rev@x"), StatusInReview, []string{"registry_invalid"}},
		{"no ledger allows self accept with notes", Ledger{State: LedgerNone}, mk("dev@example.org", "dev@example.org"), StatusCompleted, []string{"self_accept", "unverified_accept"}},
	}
	for _, c := range cases {
		r := deriveWith(c.ledger, nil, c.wp)[id(1)]
		if r.DerivedStatus != c.status || !reflect.DeepEqual(r.Warnings, c.warn) {
			t.Errorf("%s: %s %v, want %s %v", c.name, r.DerivedStatus, r.Warnings, c.status, c.warn)
		}
	}
	reg := map[LedgerState][2]bool{LedgerComplete: {true, true}, LedgerNone: {true, true}, LedgerIncomplete: {false, false}, LedgerInvalid: {true, false}}
	for state, want := range reg {
		r := deriveWith(Ledger{State: state}, nil, wp(1, "work"))[id(1)]
		if r.RegistryHistoryComplete != want[0] || r.RegistryValid != want[1] || r.Verified {
			t.Errorf("ledger %d: %v %v %v", state, r.RegistryHistoryComplete, r.RegistryValid, r.Verified)
		}
	}
}

// §11-3 normative example: self waive in unsigned mode.
func TestSelfWaiveWithoutLedger(t *testing.T) {
	p := wp(1, "work")
	submit(p, 10, "dev@example.org", "rev@example.org")
	waive(p, 11, 2, "dev@example.org")
	f := finding(2, 1, 10)
	r := derive(p, f)[id(1)]
	if r.DerivedStatus != StatusInReview || len(r.BlockedReasons) != 0 || !reflect.DeepEqual(r.Warnings, []string{"self_waive", "unverified_waive"}) ||
		r.Verified || !r.RegistryHistoryComplete || !r.RegistryValid {
		t.Fatalf("got %+v", r)
	}
	fields := r.Fields()
	if fields["derived_status"] != "InReview" || len(fields["blocked_reasons"].([]any)) != 0 {
		t.Fatalf("fields %v", fields)
	}
}

// §11-6: finding against S1 while S2 is approved.
func TestFindingAndResubmission(t *testing.T) {
	build := func(resubmit, findingDone bool) map[string]Result {
		p := wp(1, "work")
		submit(p, 10, "dev@example.org")
		if resubmit {
			submit(p, 12, "dev@example.org")
			accept(p, 13, 12, "rev@example.org")
		} else {
			accept(p, 11, 10, "rev@example.org")
		}
		f := finding(2, 1, 10)
		if findingDone {
			done(f, 20)
		}
		return derive(p, f)
	}
	r := build(true, false)[id(1)]
	if r.DerivedStatus != StatusBlocked || !reflect.DeepEqual(reasons(r), []string{"open_finding:" + id(2)}) || !*r.BlockedReasons[0].Accepted {
		t.Fatalf("S2 approved, F open: %s %v", r.DerivedStatus, reasons(r))
	}
	if r := build(true, true)[id(1)]; r.DerivedStatus != StatusCompleted {
		t.Fatalf("S2 approved, F done: %s %v", r.DerivedStatus, reasons(r))
	}
	if r := build(false, true)[id(1)]; r.DerivedStatus != StatusBlocked {
		t.Fatalf("S1 only, F done: %s", r.DerivedStatus)
	}
	// Cancelling the finding also resolves it; orphaned findings never block.
	p := done(wp(1, "work"), 10)
	f := finding(2, 1, 10)
	cancel(f)
	orphan := finding(3, 1, 77)
	res := derive(p, f, orphan)
	if res[id(1)].DerivedStatus != StatusCompleted || !reflect.DeepEqual(res[id(3)].Warnings, []string{"orphaned_finding"}) {
		t.Fatalf("cancelled/orphan: %s %v", res[id(1)].DerivedStatus, res[id(3)].Warnings)
	}
}

// §11-8: R_inspected, verified and incomplete_inputs.
func TestInspectedInputs(t *testing.T) {
	full := ledger()
	all := func(ids ...int) map[string]bool {
		m := map[string]bool{}
		for _, n := range ids {
			m[id(n)] = true
		}
		return m
	}
	// Container P with completed A and shallow cancelled B.
	p := wp(1, "container")
	a := done(wp(2, "work"), 20)
	a["parent_id"] = id(1)
	b := wp(3, "work")
	b["parent_id"] = id(1)
	cancel(b)
	in := Input{WPs: map[string]map[string]any{id(1): p, id(2): a, id(3): b}, Shallow: all(3), Ledger: full, RefVerified: all(1, 2, 3), Now: now}
	full.Users = []LedgerUser{{Username: "d", Active: true, Emails: []string{"dev@example.org"}}, {Username: "r", Active: true, Emails: []string{"rev@example.org"}}}
	in.Ledger = full
	r := Derive(in)[id(1)]
	if r.DerivedStatus != StatusCompleted || r.Verified || !reflect.DeepEqual(r.IncompleteInputs, []string{"workpackage:" + id(3)}) {
		t.Fatalf("container: %s %v %v", r.DerivedStatus, r.Verified, r.IncompleteInputs)
	}
	// Without the shallow child, a verified complete graph is verified.
	in.Shallow = nil
	if r := Derive(in)[id(1)]; !r.Verified {
		t.Fatalf("expected verified, got %+v", r)
	}
	// Unobserved WBS parent.
	w := wp(4, "work")
	w["parent_id"] = id(5)
	if r := derive(w)[id(4)]; r.Verified || !reflect.DeepEqual(r.IncompleteInputs, []string{"workpackage:" + id(5)}) {
		t.Fatalf("parent: %v", r.IncompleteInputs)
	}
	// Dependency chain A -> B -> C with only C shallow.
	x, y, z := wp(6, "work"), wp(7, "work"), wp(8, "work")
	deps(x, 7)
	deps(y, 8)
	if r := deriveWith(Ledger{State: LedgerNone}, all(8), x, y, z)[id(6)]; !reflect.DeepEqual(r.IncompleteInputs, []string{"workpackage:" + id(8)}) {
		t.Fatalf("chain: %v", r.IncompleteInputs)
	}
}

// §11-8: the four invalid_waive_target patterns.
func TestInvalidWaiveTargets(t *testing.T) {
	mk := func() map[string]any {
		p := wp(1, "work")
		submit(p, 10, "dev@example.org")
		waive(p, 11, 2, "rev@example.org")
		return p
	}
	notFinding := wp(2, "work")
	otherParent := finding(2, 9, 10)
	orphan := finding(2, 1, 77)
	cases := []struct {
		name       string
		others     []map[string]any
		incomplete []string
	}{
		{"unobserved", nil, []string{"workpackage:" + id(2)}},
		{"not a finding", []map[string]any{notFinding}, []string{}},
		// The other parent Q is unobserved: it is listed via the finding_of
		// edge, not because of invalid_waive_target.
		{"other parent", []map[string]any{otherParent}, []string{"workpackage:" + id(9)}},
		{"orphaned", []map[string]any{orphan}, []string{}},
	}
	for _, c := range cases {
		r := derive(append([]map[string]any{mk()}, c.others...)...)[id(1)]
		if !contains(r.Warnings, "invalid_waive_target") || r.DerivedStatus != StatusInReview {
			t.Errorf("%s: %s %v", c.name, r.DerivedStatus, r.Warnings)
		}
		if !reflect.DeepEqual(r.IncompleteInputs, c.incomplete) {
			t.Errorf("%s: incomplete %v want %v", c.name, r.IncompleteInputs, c.incomplete)
		}
	}
	// A shallow, out-of-scope target still lands in incomplete_inputs.
	r := deriveWith(Ledger{State: LedgerNone}, map[string]bool{id(2): true}, mk(), notFinding)[id(1)]
	if !reflect.DeepEqual(r.IncompleteInputs, []string{"workpackage:" + id(2)}) || r.Verified {
		t.Fatalf("shallow target: %v", r.IncompleteInputs)
	}
}

func TestUnauthorizedWaiveWithLedger(t *testing.T) {
	l := ledger(LedgerUser{Username: "d", Active: true, Emails: []string{"dev@example.org"}}, LedgerUser{Username: "r", Active: true, Emails: []string{"rev@example.org"}})
	p := wp(1, "work")
	submit(p, 10, "dev@example.org")
	accept(p, 11, 10, "rev@example.org")
	waive(p, 12, 2, "dev@example.org") // submitter waiving own finding
	f := finding(2, 1, 10)
	r := deriveWith(l, nil, p, f)[id(1)]
	if r.DerivedStatus != StatusBlocked || !contains(r.Warnings, "self_waive") {
		t.Fatalf("got %s %v", r.DerivedStatus, r.Warnings)
	}
	waive(p, 13, 2, "rev@example.org")
	if r := deriveWith(l, nil, p, f)[id(1)]; r.DerivedStatus != StatusCompleted {
		t.Fatalf("reviewer waive: %s %v", r.DerivedStatus, reasons(r))
	}
}
