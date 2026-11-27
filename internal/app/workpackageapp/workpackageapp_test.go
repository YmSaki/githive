package workpackageapp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/oklog/ulid/v2"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/app/wptree"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T, email string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	git(t, dir, "config", "user.email", email)
	git(t, dir, "config", "user.name", "T")
	return dir
}

func setEmail(t *testing.T, dir, email string) { git(t, dir, "config", "user.email", email) }

// remotePair returns a bare origin, a writer clone A and a reader clone B,
// both with the githive tracking refspec.
func remotePair(t *testing.T) (origin, a, b string) {
	t.Helper()
	origin = filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "--quiet", origin).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a, b = newRepo(t, "a@example.org"), newRepo(t, "b@example.org")
	for _, d := range []string{a, b} {
		git(t, d, "remote", "add", "origin", origin)
		git(t, d, "config", "--add", "remote.origin.fetch", "+refs/projects/*:refs/githive-remote/*")
	}
	return origin, a, b
}

func refOID(t *testing.T, dir, ref string) string {
	t.Helper()
	oid, err := gitx.New(dir).RevParse(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

// rawAppend writes env on top of parent without any admission check, for
// building corrupt or foreign histories.
func rawAppend(t *testing.T, dir, ref, parent string, events []*event.Envelope, env *event.Envelope) string {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]*event.Envelope(nil), events...), env)
	state := materialize.WorkpackageRegistry.Fold(all)
	files := map[string][]byte{"meta.json": []byte("{}\n"), "body.md": nil}
	if state.Meta != nil {
		if files, err = wptree.TreeFiles(state); err != nil {
			t.Fatal(err)
		}
	}
	sig, err := identity.Resolve(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var p plumbing.Hash
	if parent != "" {
		p = plumbing.NewHash(parent)
	}
	h, err := chain.AppendEvent(repo, p, env, env.Kind, files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(context.Background(), ref, h.String(), orZero(parent)); err != nil {
		t.Fatal(err)
	}
	return h.String()
}

func orZero(s string) string {
	if s == "" {
		return gitx.ZeroOID
	}
	return s
}

func rawEnv(kind, entity, actor string, data map[string]any) *event.Envelope {
	id, ts := idgen.NewWithTimestamp()
	return &event.Envelope{V: event.SchemaVersion, Kind: kind, ID: id, TS: ts, Actor: actor, Entity: entity, Data: data, Extra: map[string]any{}}
}

func conflictCode(err error) string {
	var c *ConflictError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

// §11-2: acceptance is invalidated by a body edit and re-accept is refused.
func TestAcceptanceVersionLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	body := "v1"
	id, err := s.Create(ctx, CreateInput{Title: "t", Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("a", 40)
	s1, err := s.Submit(ctx, id, SubmitInput{Commit: &commit})
	if err != nil {
		t.Fatal(err)
	}
	setEmail(t, dir, "rev@example.org")
	if _, err := s.Accept(ctx, id, "", "ok"); err != nil {
		t.Fatal(err)
	}
	sh, err := s.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Derived["derived_status"] != "Completed" {
		t.Fatalf("status %v", sh.Derived["derived_status"])
	}
	v2 := "v2"
	if err := s.Edit(ctx, id, EditInput{Body: &v2}); err != nil {
		t.Fatal(err)
	}
	sh, _ = s.Show(ctx, id)
	if sh.Derived["derived_status"] != "InReview" || sh.Derived["stale_approval"] != true {
		t.Fatalf("after edit: %v stale=%v", sh.Derived["derived_status"], sh.Derived["stale_approval"])
	}
	if _, err := s.Accept(ctx, id, s1, ""); conflictCode(err) != "stale_acceptance_version" {
		t.Fatalf("re-accept: %v", err)
	}
	setEmail(t, dir, "dev@example.org")
	s2, err := s.Submit(ctx, id, SubmitInput{NoCommit: true})
	if err != nil {
		t.Fatal(err)
	}
	setEmail(t, dir, "rev@example.org")
	if _, err := s.Accept(ctx, id, s1, ""); conflictCode(err) != "stale_submission" {
		t.Fatalf("old submission: %v", err)
	}
	if _, err := s.Accept(ctx, id, s2[:10], ""); err != nil {
		t.Fatal(err)
	}
	if sh, _ = s.Show(ctx, id); sh.Derived["derived_status"] != "Completed" {
		t.Fatalf("final %v", sh.Derived["derived_status"])
	}
	if _, err := s.Accept(ctx, id, "zzzzzzzz", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown submit id: %v", err)
	}
	if _, err := s.Accept(ctx, id, "0123", ""); err == nil {
		t.Fatal("short prefix accepted")
	}
}

// §11-4: holder's release after expiry, special-character actor, byte-exact note.
func TestExpiredReleaseNoteBytes(t *testing.T) {
	ctx := context.Background()
	actor := `q"x\y+&z@example.org`
	dir := newRepo(t, actor)
	s := New(dir)
	id, err := s.Create(ctx, CreateInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, id, StartOptions{Lease: ptrDuration(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := s.Renew(ctx, id, "", time.Hour, ""); err == nil {
		t.Fatal("renew of an expired claim must fail")
	}
	if _, err := s.Release(ctx, id, "", "終了"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Release(ctx, id, "", "again"); err == nil {
		t.Fatal("second release must fail")
	}
	state, err := s.fold(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var noteID string
	for nid := range state.Collections["notes"] {
		noteID = nid
	}
	note := state.Collections["notes"][noteID].(map[string]any)
	files, err := chain.ReadTree(mustRepo(t, dir), plumbing.NewHash(refOID(t, dir, RefFor(id).String())))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nevent_id: \"" + noteID + "\"\nkind: \"workpackage.release\"\nactor: " + `"q\"x\\y+&z@example.org"` + "\nts: \"" + note["ts"].(string) + "\"\n---\n\n終了\n"
	if got := string(files["notes/"+noteID+".md"]); got != want {
		t.Fatalf("note bytes:\n%q\nwant\n%q", got, want)
	}
	claims := state.Meta["claims"].([]any)
	if claims[0].(map[string]any)["released_at"] == nil {
		t.Fatal("expired claim was not released")
	}
}

func mustRepo(t *testing.T, dir string) *gogit.Repository {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// §11-9: dep --remove repairs cycles without lookups; adds check cycles.
func TestDependencyRules(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	a, _ := s.Create(ctx, CreateInput{Title: "a"})
	b, _ := s.Create(ctx, CreateInput{Title: "b"})
	if err := s.Dep(ctx, a, b, false); err != nil {
		t.Fatal(err)
	}
	var inv *event.InvalidInputError
	if err := s.Dep(ctx, b, a, false); !errors.As(err, &inv) {
		t.Fatalf("cycle not rejected: %v", err)
	}
	unfetched := strings.ToUpper(idgen.New())
	if err := s.Dep(ctx, a, unfetched, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown add: %v", err)
	}
	// Create an existing unrelated cycle directly, then remove an unfetched dep.
	c, _ := s.Create(ctx, CreateInput{Title: "c"})
	d, _ := s.Create(ctx, CreateInput{Title: "d", DependsOn: []string{c}})
	if _, err := s.append(ctx, c, "dependency", "", map[string]any{"target": d}); err != nil {
		t.Fatal(err)
	}
	ghost := idgen.New()
	if _, err := s.append(ctx, a, "dependency", "", map[string]any{"target": ghost}); err != nil {
		t.Fatal(err)
	}
	if err := s.Dep(ctx, a, ghost, true); err != nil {
		t.Fatalf("repair removal: %v", err)
	}
	state, _ := s.fold(ctx, a)
	if deps := state.Meta["depends_on"].([]any); len(deps) != 1 || deps[0] != b {
		t.Fatalf("depends_on %v", deps)
	}
}

// §11-5: a second start on the same local ref is claim_held.
func TestDoubleStartClaimHeld(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	id, _ := s.Create(ctx, CreateInput{Title: "t"})
	res, err := s.Start(ctx, id, StartOptions{Branch: "feature/x"})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.Outcome(ctx, id, res.ClaimID, false); err != nil || out.ActiveClaim["branch"] != "feature/x" {
		t.Fatalf("outcome %v %v", out, err)
	}
	before := refOID(t, dir, RefFor(id).String())
	if _, err := s.Start(ctx, id, StartOptions{}); conflictCode(err) != "claim_held" {
		t.Fatalf("second start: %v", err)
	}
	if refOID(t, dir, RefFor(id).String()) != before {
		t.Fatal("rejected start moved the ref")
	}
	setEmail(t, dir, "other@example.org")
	if err := s.Cancel(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, id, StartOptions{}); conflictCode(err) != "already_cancelled" {
		t.Fatalf("start on cancelled: %v", err)
	}
}

func TestOutcomeClaimLost(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "first@example.org")
	s := New(dir)
	id, _ := s.Create(ctx, CreateInput{Title: "t"})
	first, err := s.Start(ctx, id, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	setEmail(t, dir, "second@example.org")
	// A merged concurrent claim: appended without start's precheck.
	state, err := s.append(ctx, id, "claim", "", map[string]any{"lease_until": "2099-01-01T00:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	claims := state.Meta["claims"].([]any)
	mine := claims[len(claims)-1].(map[string]any)["claim_id"].(string)
	if _, err := s.Outcome(ctx, id, mine, true); conflictCode(err) != "claim_lost" {
		t.Fatalf("got %v", err)
	}
	if out, err := s.Outcome(ctx, id, first.ClaimID, true); err != nil || out.ClaimID != first.ClaimID {
		t.Fatalf("winner: %v %v", out, err)
	}
}

// §11-5 initial adoption: the claim commit sits on the tracking tip and the
// local ref is created in one CAS.
func TestStartAdoptsTrackingOnlyWorkpackage(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	id, err := New(a).Create(ctx, CreateInput{Title: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	sb := New(b)
	res, err := sb.Start(ctx, id[:10], StartOptions{Fetch: true, Remote: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if res.PrefetchErr != nil {
		t.Fatal(res.PrefetchErr)
	}
	tracking := refOID(t, b, "refs/githive-remote/workpackage/"+id)
	local := refOID(t, b, RefFor(id).String())
	if parent := git(t, b, "rev-parse", local+"^"); parent != tracking {
		t.Fatalf("claim parent %s, want tracking tip %s", parent, tracking)
	}
	if git(t, b, "rev-list", "--count", local) != "2" {
		t.Fatal("unexpected history length")
	}
}

// §11-5 clock_skew on adoption: a claim sorting before the tracking history.
func TestStartAdoptionClockSkew(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	wid := strings.ToLower(ulid.MustNew(ulid.Timestamp(future), bytes.NewReader(make([]byte, 10))).String())
	create := &event.Envelope{V: event.SchemaVersion, Kind: "workpackage.create", ID: wid, Entity: wid,
		TS: future.Format(materialize.WorkpackageTimeLayout), Actor: "a@example.org", Data: map[string]any{"title": "future"}, Extra: map[string]any{}}
	tip := rawAppend(t, a, RefFor(wid).String(), "", nil, create)
	// A live tracking claim too: history checks (clock_skew) precede the
	// state precheck (claim_held), spec §9 step 2-3.
	at := future.Add(time.Millisecond)
	cid := strings.ToLower(ulid.MustNew(ulid.Timestamp(at), bytes.NewReader(make([]byte, 10))).String())
	claim := &event.Envelope{V: event.SchemaVersion, Kind: "workpackage.claim", ID: cid, Entity: wid,
		TS: at.Format(materialize.WorkpackageTimeLayout), Actor: "a@example.org",
		Data: map[string]any{"lease_until": at.Add(2 * time.Hour).Format(materialize.WorkpackageTimeLayout)}, Extra: map[string]any{}}
	rawAppend(t, a, RefFor(wid).String(), tip, []*event.Envelope{create}, claim)
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	_, err := New(b).Start(ctx, wid, StartOptions{Fetch: true, Remote: "origin"})
	var skew *entitychain.ClockSkewError
	if !errors.As(err, &skew) {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(wid).String()) != "" {
		t.Fatal("local ref created despite clock skew")
	}
}

// §11-5 (f): a live claim seen only on the tracking side rejects start
// without touching the local ref.
func TestStartClaimHeldOnTrackingSide(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	sa := New(a)
	id, _ := sa.Create(ctx, CreateInput{Title: "t"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	git(t, b, "fetch", "origin", "+refs/projects/*:refs/projects/*")
	before := refOID(t, b, RefFor(id).String())
	if _, err := sa.Start(ctx, id, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := New(b).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); conflictCode(err) != "claim_held" {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(id).String()) != before {
		t.Fatal("local ref moved")
	}
	// A release seen only on the tracking side lets start proceed.
	if _, err := sa.Release(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := New(b).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); err != nil {
		t.Fatalf("after remote release: %v", err)
	}
}

func TestStartInvalidRemoteAndPrefetchFailure(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	id, _ := s.Create(ctx, CreateInput{Title: "t"})
	before := refOID(t, dir, RefFor(id).String())
	if _, err := s.Start(ctx, id, StartOptions{Fetch: true, Remote: "nosuch"}); !errors.Is(err, gitx.ErrInvalidRemote) {
		t.Fatalf("got %v", err)
	}
	if refOID(t, dir, RefFor(id).String()) != before {
		t.Fatal("invalid remote moved the ref")
	}
	git(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
	res, err := s.Start(ctx, id, StartOptions{Fetch: true, Remote: "broken"})
	if err != nil || res.PrefetchErr == nil {
		t.Fatalf("prefetch failure must continue locally: %v %v", res, err)
	}
}

// §11 create-count contract: note-only / checkpoint-only histories and
// create-less tips on either side are rejected everywhere.
func TestCreateLessHistoriesRejected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		kind string
		data map[string]any
	}{
		{"note-only", "workpackage.note", map[string]any{"body": "orphan"}},
		{"checkpoint-only", "workpackage.checkpoint", map[string]any{"state": map[string]any{"meta": nil, "collections": map[string]any{}, "scratch": map[string]any{}}, "event_count": 0, "head": "x", "max_event_id": "01jzc0wp0000000000000000cp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, a, b := remotePair(t)
			wid := idgen.New()
			rawAppend(t, a, RefFor(wid).String(), "", nil, rawEnv(tc.kind, wid, "a@example.org", tc.data))
			report, err := fsckapp.New(a).Run(ctx, fsckapp.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !hasFinding(report, "event_order_violation") {
				t.Fatalf("fsck findings: %+v", report.Findings)
			}
			vr, err := verifyapp.VerifyRef(ctx, a, RefFor(wid).String(), "")
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(vr.Issues, "event_order_violation") {
				t.Fatalf("verify issues %+v", vr.Issues)
			}
			git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
			_, err = syncapp.Sync(ctx, b, "origin", []string{RefFor(wid).String()}, 0)
			if !errors.Is(err, event.ErrEventOrderViolation) {
				t.Fatalf("sync: %v", err)
			}
			if refOID(t, b, RefFor(wid).String()) != "" {
				t.Fatal("sync adopted a create-less history")
			}
			if _, err := New(b).Start(ctx, wid, StartOptions{Fetch: true, Remote: "origin"}); !errors.Is(err, event.ErrEventOrderViolation) {
				t.Fatalf("start: %v", err)
			}
			if refOID(t, b, RefFor(wid).String()) != "" {
				t.Fatal("start created the ref")
			}
		})
	}
}

// §11 left/right create-less tips: union has one create, but one tip lacks it.
func TestCreateLessTipEitherSide(t *testing.T) {
	ctx := context.Background()
	for _, localHasCreate := range []bool{false, true} {
		_, a, b := remotePair(t)
		wid := idgen.New()
		create := rawEnv("workpackage.create", wid, "a@example.org", map[string]any{"title": "t"})
		note := rawEnv("workpackage.note", wid, "b@example.org", map[string]any{"body": "n"})
		remoteEnv, localEnv := create, note
		if localHasCreate {
			remoteEnv, localEnv = note, create
		}
		rawAppend(t, a, RefFor(wid).String(), "", nil, remoteEnv)
		git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
		rawAppend(t, b, RefFor(wid).String(), "", nil, localEnv)
		before := refOID(t, b, RefFor(wid).String())
		if _, err := New(b).Start(ctx, wid, StartOptions{Fetch: true, Remote: "origin"}); !errors.Is(err, event.ErrEventOrderViolation) {
			t.Fatalf("localHasCreate=%v start: %v", localHasCreate, err)
		}
		if _, err := syncapp.Sync(ctx, b, "origin", []string{RefFor(wid).String()}, 0); !errors.Is(err, event.ErrEventOrderViolation) {
			t.Fatalf("localHasCreate=%v sync: %v", localHasCreate, err)
		}
		if refOID(t, b, RefFor(wid).String()) != before {
			t.Fatal("ref moved")
		}
	}
}

// §11 data contracts: a known kind with invalid data is refused by sync
// adoption and by start's adoption; unknown kinds stay forward compatible.
func TestDataContractsAndUnknownKinds(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	wid := idgen.New()
	create := rawEnv("workpackage.create", wid, "a@example.org", map[string]any{"title": "t"})
	tip := rawAppend(t, a, RefFor(wid).String(), "", nil, create)
	bad := rawEnv("workpackage.claim", wid, "a@example.org", map[string]any{"branch": "x"})
	rawAppend(t, a, RefFor(wid).String(), tip, []*event.Envelope{create}, bad)
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	var schemaErr *event.DataSchemaError
	if _, err := syncapp.Sync(ctx, b, "origin", []string{RefFor(wid).String()}, 0); !errors.As(err, &schemaErr) {
		t.Fatalf("sync: %v", err)
	}
	if _, err := New(b).Start(ctx, wid, StartOptions{Fetch: true, Remote: "origin"}); !errors.As(err, &schemaErr) {
		t.Fatalf("start: %v", err)
	}
	if refOID(t, b, RefFor(wid).String()) != "" {
		t.Fatal("invalid history adopted")
	}

	// Unknown kind.
	other := idgen.New()
	c2 := rawEnv("workpackage.create", other, "a@example.org", map[string]any{"title": "u"})
	tip2 := rawAppend(t, a, RefFor(other).String(), "", nil, c2)
	future := rawEnv("workpackage.future_kind", other, "a@example.org", map[string]any{"x": 1})
	rawAppend(t, a, RefFor(other).String(), tip2, []*event.Envelope{c2}, future)
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := syncapp.Sync(ctx, b, "origin", []string{RefFor(other).String()}, 0); err != nil {
		t.Fatalf("unknown kind sync: %v", err)
	}
	sh, err := New(b).Show(ctx, other)
	if err != nil || sh.Meta["updated_at"] != c2.TS {
		t.Fatalf("unknown kind must not change state: %v %v", sh, err)
	}
	report, err := fsckapp.New(b).Run(ctx, fsckapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(report, "unknown_event_kind") || hasFinding(report, "tree_mismatch") {
		t.Fatalf("fsck %+v", report.Findings)
	}
}

// §11-1 / ① left-right merge: concurrent edit/submit/claim on two clones
// converge to byte-identical trees after sync both ways.
func TestConcurrentMergeConverges(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	sa, sb := New(a), New(b)
	id, _ := sa.Create(ctx, CreateInput{Title: "t"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := syncapp.Sync(ctx, b, "origin", []string{RefFor(id).String()}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Submit(ctx, id, SubmitInput{NoCommit: true}); err != nil {
		t.Fatal(err)
	}
	v2 := "v2"
	if err := sb.Edit(ctx, id, EditInput{Body: &v2}); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Release(ctx, id, "", ""); err == nil {
		t.Fatal("release without a claim must fail")
	}
	for _, d := range []string{a, b, a} {
		if _, err := syncapp.Sync(ctx, d, "origin", []string{RefFor(id).String()}, 3); err != nil {
			t.Fatalf("sync %s: %v", d, err)
		}
	}
	ta := git(t, a, "rev-parse", RefFor(id).String()+"^{tree}")
	tb := git(t, b, "rev-parse", RefFor(id).String()+"^{tree}")
	if ta != tb {
		t.Fatal("trees differ after merge")
	}
	sh, _ := sa.Show(ctx, id)
	if sh.Derived["derived_status"] != "InReview" || sh.Body != "v2" {
		t.Fatalf("merged %v %q", sh.Derived["derived_status"], sh.Body)
	}
}

// §11-7: fsck --compact checkpoints a workpackage chain; reads still match.
func TestCompactCheckpointTransparent(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	id, _ := s.Create(ctx, CreateInput{Title: "t"})
	for _, b := range []string{"x", "x\n"} {
		if err := s.Note(ctx, id, b); err != nil {
			t.Fatal(err)
		}
	}
	treeBefore := git(t, dir, "rev-parse", RefFor(id).String()+"^{tree}")
	report, err := fsckapp.New(dir).Run(ctx, fsckapp.Options{Compact: true, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checkpoints) == 0 {
		t.Fatalf("no checkpoint: %+v", report.Findings)
	}
	if git(t, dir, "rev-parse", RefFor(id).String()+"^{tree}") != treeBefore {
		t.Fatal("checkpoint changed the tree")
	}
	if err := s.Note(ctx, id, "after"); err != nil {
		t.Fatal(err)
	}
	newBody := "changed after checkpoint"
	if err := s.Edit(ctx, id, EditInput{Body: &newBody}); err != nil {
		t.Fatal(err)
	}
	if sh, err := s.Show(ctx, id); err != nil || sh.Body != newBody || len(sh.Notes) != 3 {
		t.Fatalf("show after checkpoint: %v %v", sh, err)
	}
	if _, err := s.Start(ctx, id, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Renew(ctx, id, "", 2*time.Hour, "b"); err != nil {
		t.Fatal(err)
	}
	report, err = fsckapp.New(dir).Run(ctx, fsckapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Findings {
		if f.Severity != fsckapp.SeverityWarning {
			t.Fatalf("finding %+v", f)
		}
	}
}

func hasFinding(r *fsckapp.Report, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func hasIssue(issues []verifyapp.Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// §9 step 2-1 order: a cross-tip ID collision is reported before the
// create-order violation of a create-less local tip.
func TestStartIDCollisionBeforeOrderCheck(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	wid := idgen.New()
	create := rawEnv("workpackage.create", wid, "a@example.org", map[string]any{"title": "t"})
	tip := rawAppend(t, a, RefFor(wid).String(), "", nil, create)
	note := rawEnv("workpackage.note", wid, "a@example.org", map[string]any{"body": "remote"})
	rawAppend(t, a, RefFor(wid).String(), tip, []*event.Envelope{create}, note)
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	forged := *note
	forged.Data = map[string]any{"body": "local"}
	rawAppend(t, b, RefFor(wid).String(), "", nil, &forged)
	before := refOID(t, b, RefFor(wid).String())
	if _, err := New(b).Start(ctx, wid, StartOptions{Fetch: true, Remote: "origin"}); !errors.Is(err, event.ErrConflictingEventID) {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(wid).String()) != before {
		t.Fatal("ref moved")
	}
}

// §9 step 1: the snapshot is read from start's private fetch namespace,
// which never outlives the call.
func TestStartSnapshotNamespaceCleaned(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	id, _ := New(a).Create(ctx, CreateInput{Title: "t"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := New(b).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); err != nil {
		t.Fatal(err)
	}
	if left := git(t, b, "for-each-ref", "refs/githive-snapshot/"); left != "" {
		t.Fatalf("private snapshot refs left behind:\n%s", left)
	}
	if refOID(t, b, "refs/githive-remote/workpackage/"+id) == "" {
		t.Fatal("tracking ref not updated by the pre-fetch")
	}
}

// §11-5 (d): an incompatible tracking meta/config rejects start; an
// ancestor/equal config passes.
func TestStartMetaConfigConflict(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	if _, err := initapp.Init(ctx, a, "origin", "proj-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := initapp.Init(ctx, b, "origin", "proj-b"); err != nil {
		t.Fatal(err)
	}
	id, _ := New(a).Create(ctx, CreateInput{Title: "t"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	if _, err := New(b).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); !errors.Is(err, syncapp.ErrMetaConfigConflict) {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(id).String()) != "" {
		t.Fatal("conflicting start created the ref")
	}
	_, _, c := remotePair(t)
	git(t, c, "remote", "set-url", "origin", git(t, a, "remote", "get-url", "origin"))
	if _, err := initapp.Init(ctx, c, "origin", "proj-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); err != nil {
		t.Fatalf("shared config: %v", err)
	}
}

// §11-5 (g): a concurrent fetch that prunes the tracking ref right after
// start's own fetch cannot erase the claim start already observed.
func TestStartSnapshotSurvivesTrackingRewind(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	sa := New(a)
	id, _ := sa.Create(ctx, CreateInput{Title: "t"})
	if _, err := sa.Start(ctx, id, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	sb := New(b)
	sb.afterPrefetch = func() {
		git(t, b, "update-ref", "-d", "refs/githive-remote/workpackage/"+id)
	}
	if _, err := sb.Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"}); conflictCode(err) != "claim_held" {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(id).String()) != "" {
		t.Fatal("rejected start created the local ref")
	}
}

// §11-5 (e): an unrelated shallow ref blocks start even when the target WP
// exists only, and completely, on the tracking side.
func TestStartUnrelatedShallowRef(t *testing.T) {
	ctx := context.Background()
	_, a, b := remotePair(t)
	tid := idgen.New()
	tref := "refs/projects/task/" + tid
	c1 := rawEnv("task.create", tid, "a@example.org", map[string]any{"title": "t"})
	tip := rawAppend(t, a, tref, "", nil, c1)
	rawAppend(t, a, tref, tip, []*event.Envelope{c1}, rawEnv("task.note", tid, "a@example.org", map[string]any{"body": "n"}))
	id, _ := New(a).Create(ctx, CreateInput{Title: "wp"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	git(t, b, "fetch", "--depth", "1", "origin", "+"+tref+":"+tref)
	_, err := New(b).Start(ctx, id, StartOptions{Fetch: true, Remote: "origin"})
	var shallow *chain.ShallowHistoryError
	if !errors.As(err, &shallow) {
		t.Fatalf("got %v", err)
	}
	if refOID(t, b, RefFor(id).String()) != "" {
		t.Fatal("start created the WP ref")
	}
}

// §11-7 at app level: a checkpoint whose ID sorts before create does not
// break create-order admission, reads or later claims.
func TestCheckpointSortingBeforeCreate(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	wid := idgen.New()
	cp := rawEnv("workpackage.checkpoint", wid, "dev@example.org", map[string]any{
		"state":       map[string]any{"meta": nil, "collections": map[string]any{}, "scratch": map[string]any{}},
		"event_count": 0, "head": "x", "max_event_id": wid})
	time.Sleep(2 * time.Millisecond)
	create := rawEnv("workpackage.create", wid, "dev@example.org", map[string]any{"title": "cp"})
	tip := rawAppend(t, dir, RefFor(wid).String(), "", nil, create)
	rawAppend(t, dir, RefFor(wid).String(), tip, []*event.Envelope{create}, cp)
	s := New(dir)
	if _, err := s.Start(ctx, wid, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if sh, err := s.Show(ctx, wid); err != nil || sh.Meta["title"] != "cp" || sh.Derived["derived_status"] != "Active" {
		t.Fatalf("show %v %v", sh, err)
	}
	report, err := fsckapp.New(dir).Run(ctx, fsckapp.Options{})
	if err != nil || hasFinding(report, "event_order_violation") {
		t.Fatalf("fsck %+v %v", report, err)
	}
}

func ptrDuration(d time.Duration) *time.Duration { return &d }

// Lease input boundary: omitted means DefaultLease; an explicit 0s or a
// sub-millisecond lease is invalid_input before anything is written; 1ms is
// the smallest lease whose lease_until lands strictly after the claim ts.
func TestStartLeaseBoundary(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	for _, tc := range []struct {
		name  string
		lease *time.Duration
		ok    bool
	}{
		{"omitted", nil, true},
		{"0s", ptrDuration(0), false},
		{"1ns", ptrDuration(time.Nanosecond), false},
		{"999us", ptrDuration(999 * time.Microsecond), false},
		{"1ms", ptrDuration(time.Millisecond), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := s.Create(ctx, CreateInput{Title: tc.name})
			if err != nil {
				t.Fatal(err)
			}
			before := refOID(t, dir, RefFor(id).String())
			_, err = s.Start(ctx, id, StartOptions{Lease: tc.lease})
			if !tc.ok {
				var invalid *event.InvalidInputError
				if !errors.As(err, &invalid) {
					t.Fatalf("want invalid_input, got %v", err)
				}
				if refOID(t, dir, RefFor(id).String()) != before {
					t.Fatal("rejected lease moved the ref")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.fold(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			claims := claimList(state.Meta)
			if len(claims) != 1 {
				t.Fatalf("claims %v", claims)
			}
			ts, _ := time.Parse(time.RFC3339Nano, claims[0]["ts"].(string))
			until, _ := time.Parse(time.RFC3339Nano, claims[0]["lease_until"].(string))
			want := DefaultLease
			if tc.lease != nil {
				want = *tc.lease
			}
			if got := until.Sub(ts); got != want {
				t.Fatalf("lease_until - ts = %v, want %v", got, want)
			}
		})
	}
	// Renew shares the same boundary.
	id, _ := s.Create(ctx, CreateInput{Title: "renew"})
	if _, err := s.Start(ctx, id, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	var invalid *event.InvalidInputError
	if _, err := s.Renew(ctx, id, "", time.Nanosecond, ""); !errors.As(err, &invalid) {
		t.Fatalf("renew 1ns: %v", err)
	}
}

// A read folds the tips its single for-each-ref enumerated: a ref rewound or
// deleted between enumeration and fold does not change the view.
func TestObservePinsEnumeratedOIDs(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t, "dev@example.org")
	s := New(dir)
	id, err := s.Create(ctx, CreateInput{Title: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := s.Create(ctx, CreateInput{Title: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	v1 := refOID(t, dir, RefFor(id).String())
	v2 := "v2"
	if err := s.Edit(ctx, id, EditInput{Title: &v2}); err != nil {
		t.Fatal(err)
	}
	v2OID := refOID(t, dir, RefFor(id).String())
	goneOID := refOID(t, dir, RefFor(gone).String())
	s.afterRefList = func() {
		git(t, dir, "update-ref", RefFor(id).String(), v1, v2OID)
		git(t, dir, "update-ref", "-d", RefFor(gone).String(), goneOID)
	}
	res, err := s.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]any{}
	for _, it := range res.Items {
		titles[it["id"].(string)] = it["title"]
	}
	if titles[id] != "v2" || titles[gone] != "gone" {
		t.Fatalf("list mixed read moments: %v", titles)
	}

	// Show: same guarantee (restore, then rewind during the read).
	git(t, dir, "update-ref", RefFor(id).String(), v2OID, v1)
	s.afterRefList = func() { git(t, dir, "update-ref", RefFor(id).String(), v1, v2OID) }
	sh, err := s.Show(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Meta["title"] != "v2" {
		t.Fatalf("show title %v", sh.Meta["title"])
	}
}

// The shallow snapshot fallback reads the enumerated OID too: pointing the
// ref at another workpackage's commit mid-read neither changes the view nor
// trips the snapshot's meta/ref ID check.
func TestObservePinsShallowSnapshotOID(t *testing.T) {
	ctx := context.Background()
	origin, a, _ := remotePair(t)
	deep, _ := New(a).Create(ctx, CreateInput{Title: "deep"})
	if err := New(a).Note(ctx, deep, "second"); err != nil {
		t.Fatal(err)
	}
	other, _ := New(a).Create(ctx, CreateInput{Title: "other"})
	git(t, a, "push", "origin", "refs/projects/*:refs/projects/*")
	dir := newRepo(t, "dev@example.org")
	git(t, dir, "remote", "add", "origin", origin)
	git(t, dir, "fetch", "--depth", "1", "origin", "+refs/projects/*:refs/projects/*")
	s := New(dir)
	deepOID := refOID(t, dir, RefFor(deep).String())
	otherOID := refOID(t, dir, RefFor(other).String())
	s.afterRefList = func() { git(t, dir, "update-ref", RefFor(deep).String(), otherOID, deepOID) }
	sh, err := s.Show(ctx, deep)
	if err != nil {
		t.Fatal(err)
	}
	if sh.ReadSource != SourceSnapshot || sh.Meta["title"] != "deep" {
		t.Fatalf("shallow show %v %v", sh.ReadSource, sh.Meta["title"])
	}
}
