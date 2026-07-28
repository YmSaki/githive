package fsckapp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "tester@example.com"}, {"user.name", "Tester"}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return dir
}

// appendNotifyShardEvent appends a notify.post event directly onto
// shardRef (a refs/projects/notify/<yyyy-mm> monthly shard, D8), bypassing
// notifyapp.Service, which is intentionally still hard-coded to the stream
// ref (notifyapp.go「シャード読み手の集約は移行時に実装」). It uses
// entitychain.Writer directly - the same primitive notifyapp.Service uses
// internally - to build a chain fsck/sync/verify can be pointed at ahead of
// the reader-side migration.
func appendNotifyShardEvent(t *testing.T, dir, shardRef, title string) {
	t.Helper()
	w := &entitychain.Writer{
		Dir:       dir,
		RefFor:    func(string) plumbing.ReferenceName { return plumbing.ReferenceName(shardRef) },
		Registry:  materialize.NotifyRegistry,
		TreeFiles: notifyapp.TreeFiles,
	}
	_, err := w.Append(context.Background(), "", func() (*event.Envelope, string) {
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: event.SchemaVersion, Kind: "notify.post", ID: eid, TS: ts,
			Entity: eid,
			Data:   map[string]any{"targets": []any{"all"}, "title": title},
			Extra:  map[string]any{},
		}, "notify.post: " + title
	})
	if err != nil {
		t.Fatal(err)
	}
}

// chatRefState folds a chat chain and returns a canonical-JSON signature of
// its materialized state, so a before/after comparison can assert a checkpoint
// left the fold result unchanged.
func chatRefState(t *testing.T, dir, id string) string {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(context.Background(), "refs/projects/chat/"+id)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	state := materialize.ChatRegistry.Fold(events)
	stateMap, err := materialize.StateToMap(state)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := event.EncodeString(stateMap)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestValidTreePasses(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	if _, err := chatapp.New(dir).NewThread(ctx, "release plan", "let's write it down"); err != nil {
		t.Fatal(err)
	}
	if _, err := notifyapp.New(dir).Post(ctx, []string{"all"}, "heads up", "body", nil, ""); err != nil {
		t.Fatal(err)
	}

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("expected clean fsck, got findings: %+v", report.Findings)
	}
	if len(report.Checkpoints) != 0 {
		t.Errorf("no --compact: expected no checkpoints, got %+v", report.Checkpoints)
	}
}

func TestCorruptEnvelopeReported(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	id, err := chatapp.New(dir).NewThread(ctx, "t", "hi")
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/chat/" + id

	// Append a commit whose message body is not a valid event envelope,
	// bypassing the validating write path (chain.AppendEvent) on purpose.
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	parent := plumbing.NewHash(oid)
	parentCommit, err := object.GetCommit(repo.Storer, parent)
	if err != nil {
		t.Fatal(err)
	}
	sig := object.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	bad := &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      "bad commit\n\n{ this is not valid json",
		TreeHash:     parentCommit.TreeHash,
		ParentHashes: []plumbing.Hash{parent},
	}
	encoded := repo.Storer.NewEncodedObject()
	if err := bad.Encode(encoded); err != nil {
		t.Fatal(err)
	}
	badHash, err := repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, ref, badHash.String(), oid); err != nil {
		t.Fatal(err)
	}

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("expected a finding for the corrupt envelope")
	}
	found := false
	for _, f := range report.Findings {
		if f.Code == "invalid_envelope" && f.Commit == badHash.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid_envelope finding on %s, got %+v", badHash, report.Findings)
	}
}

// TestNotifyMonthlyShardValidatedAsNotifyChain is D8's fsck goal: a
// hand-built refs/projects/notify/2026-07 shard chain must be picked up by
// refspace.Parse (Feature notify) and validated exactly like notify/stream
// - a clean chain passes, and a corrupted envelope on the shard is reported
// with the shard ref attached, proving it is actually walked and not
// silently skipped the way an unrecognized ref is (see
// TestUnrecognizedRefReportedAsWarning).
func TestNotifyMonthlyShardValidatedAsNotifyChain(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	shardRef := "refs/projects/notify/2026-07"

	appendNotifyShardEvent(t, dir, shardRef, "shard notice")

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("expected a valid notify shard chain to pass fsck cleanly, got findings: %+v", report.Findings)
	}

	// Corrupt the shard's tip commit the same way TestCorruptEnvelopeReported
	// does for chat, to prove the shard ref is actually schema-validated.
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(ctx, shardRef)
	if err != nil {
		t.Fatal(err)
	}
	parent := plumbing.NewHash(oid)
	parentCommit, err := object.GetCommit(repo.Storer, parent)
	if err != nil {
		t.Fatal(err)
	}
	sig := object.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	bad := &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      "bad shard commit\n\n{ this is not valid json",
		TreeHash:     parentCommit.TreeHash,
		ParentHashes: []plumbing.Hash{parent},
	}
	encoded := repo.Storer.NewEncodedObject()
	if err := bad.Encode(encoded); err != nil {
		t.Fatal(err)
	}
	badHash, err := repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, shardRef, badHash.String(), oid); err != nil {
		t.Fatal(err)
	}

	report, err = New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		if f.Ref == shardRef && f.Code == "invalid_envelope" && f.Commit == badHash.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid_envelope finding on shard ref %s commit %s, got %+v", shardRef, badHash, report.Findings)
	}
}

// TestUnrecognizedRefReportedAsWarning is D8 item 4: a ref under
// refs/projects/ that refspace.Parse rejects outright (here, an unknown
// feature name) used to be silently skipped by fsck; it must now surface as
// a SeverityWarning Finding naming the ref, without failing fsck's OK().
func TestUnrecognizedRefReportedAsWarning(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "--quiet", "-m", "x").CombinedOutput()
	if err != nil {
		t.Fatalf("git commit --allow-empty: %v\n%s", err, out)
	}
	headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, headOut)
	}
	head := strings.TrimSpace(string(headOut))

	bogusRef := "refs/projects/bogus/x"
	if err := gitx.New(dir).UpdateRef(ctx, bogusRef, head, gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var found *Finding
	for i, f := range report.Findings {
		if f.Ref == bogusRef {
			found = &report.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a finding for %s, got %+v", bogusRef, report.Findings)
	}
	if found.Severity != SeverityWarning {
		t.Errorf("expected severity %q for %s, got %q", SeverityWarning, bogusRef, found.Severity)
	}
	if !strings.Contains(found.Message, bogusRef) {
		t.Errorf("expected message to mention %s, got %q", bogusRef, found.Message)
	}
	if !report.OK() {
		t.Errorf("a warning-only finding must not fail fsck's OK(), findings: %+v", report.Findings)
	}
}

func TestCompactCountThreshold(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	svc := chatapp.New(dir)
	id, err := svc.NewThread(ctx, "t", "first") // chat.create (1 event)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Post(ctx, id, "second", "", ""); err != nil { // chat.post
		t.Fatal(err)
	}
	if err := svc.Post(ctx, id, "third", "", ""); err != nil { // chat.post
		t.Fatal(err)
	}

	before := chatRefState(t, dir, id)

	// Threshold of 2: 3 events since the (absent) last checkpoint trips the
	// event-count branch.
	report, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %+v", report.Checkpoints)
	}
	if report.Checkpoints[0].Reason != "event_count" {
		t.Errorf("expected reason event_count, got %q", report.Checkpoints[0].Reason)
	}
	if report.Checkpoints[0].EventCount != 3 {
		t.Errorf("expected event_count 3, got %d", report.Checkpoints[0].EventCount)
	}

	assertCheckpointOnChain(t, dir, id, "chat.checkpoint")
	if after := chatRefState(t, dir, id); after != before {
		t.Errorf("checkpoint changed materialized state:\nbefore=%s\nafter =%s", before, after)
	}
}

func TestCompactAgeThreshold(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	svc := chatapp.New(dir)
	id, err := svc.NewThread(ctx, "t", "only a couple events")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Post(ctx, id, "second", "", ""); err != nil {
		t.Fatal(err)
	}

	before := chatRefState(t, dir, id)

	// Default thresholds (500 events / 90 days), but advance the clock 100
	// days: only 2 events, so the age branch (not the count branch) must fire.
	report, err := New(dir).Run(ctx, Options{Compact: true, Now: time.Now().Add(100 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %+v", report.Checkpoints)
	}
	if report.Checkpoints[0].Reason != "age" {
		t.Errorf("expected reason age, got %q", report.Checkpoints[0].Reason)
	}

	assertCheckpointOnChain(t, dir, id, "chat.checkpoint")
	if after := chatRefState(t, dir, id); after != before {
		t.Errorf("checkpoint changed materialized state:\nbefore=%s\nafter =%s", before, after)
	}
}

func assertCheckpointOnChain(t *testing.T, dir, id, wantKind string) {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(context.Background(), "refs/projects/chat/"+id)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == wantKind {
			return
		}
	}
	t.Errorf("expected a %s event on the chain, none found", wantKind)
}

func TestWikiFilenameViolation(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()

	// Build a wiki branch (a plain git tree, not an event chain) containing a
	// Windows-reserved filename.
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	treeHash, err := chain.WriteTree(repo, map[string][]byte{
		"Home.md": []byte("# home\n"),
		"CON.md":  []byte("reserved device name\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := object.Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	c := &object.Commit{Author: sig, Committer: sig, Message: "wiki init", TreeHash: treeHash}
	encoded := repo.Storer.NewEncodedObject()
	if err := c.Encode(encoded); err != nil {
		t.Fatal(err)
	}
	h, err := repo.Storer.SetEncodedObject(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.New(dir).UpdateRef(ctx, "refs/projects/wiki/main", h.String(), gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}

	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		if f.Feature == "wiki" && f.Code == "reserved_name" && f.Path == "CON.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a wiki reserved_name finding on CON.md, got %+v", report.Findings)
	}
}

// TestCheckpointFoldTransparency proves at the fold level that an fsck-style
// checkpoint envelope does not change the materialized state (mirrors
// materialize_test.go's TestFoldCheckpointTransparency), independent of the
// git write path.
func TestCheckpointFoldTransparency(t *testing.T) {
	base := []*event.Envelope{
		{V: 1, Kind: "chat.create", ID: "01j8xq4d3nbz9k7w2m5e8h1t60", TS: "2026-07-01T00:00:00.000Z",
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t60",
			Data: map[string]any{"title": "t", "body": "hi"}, Extra: map[string]any{}},
		{V: 1, Kind: "chat.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t61", TS: "2026-07-01T00:00:01.000Z",
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t60",
			Data: map[string]any{"body": "second"}, Extra: map[string]any{}},
	}
	stateWithout := materialize.ChatRegistry.Fold(base)

	cpData, err := checkpointData(stateWithout, countNonCheckpoint(base), "deadbeef", maxEventID(base))
	if err != nil {
		t.Fatal(err)
	}
	cp := &event.Envelope{
		V: 1, Kind: "chat.checkpoint", ID: "01j8xq4d3nbz9k7w2m5e8h1t62", TS: "2026-07-01T00:00:02.000Z",
		Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t60",
		Data:  cpData,
		Extra: map[string]any{},
	}
	withCP := append(append([]*event.Envelope(nil), base...), cp)
	stateWith := materialize.ChatRegistry.Fold(withCP)

	sigWithout := foldStateSignature(t, stateWithout)
	sigWith := foldStateSignature(t, stateWith)
	if sigWithout != sigWith {
		t.Errorf("checkpoint changed fold result:\nwithout=%s\nwith   =%s", sigWithout, sigWith)
	}
}

func TestShouldCompactThresholds(t *testing.T) {
	now := time.Now()

	// --- event-count branch, at exactly the real 500 default ---
	var events []*event.Envelope
	for i := 0; i < DefaultMaxEvents; i++ {
		eid, ts := idgen.NewWithTimestamp()
		events = append(events, &event.Envelope{
			V: 1, Kind: "chat.post", ID: eid, TS: ts,
			Actor: "a@example.com", Entity: eid, Data: map[string]any{}, Extra: map[string]any{},
		})
	}
	if reason, due := shouldCompact(events, DefaultMaxEvents, DefaultMaxAge, now); !due || reason != "event_count" {
		t.Errorf("500 events: want (event_count,true), got (%q,%v)", reason, due)
	}
	if reason, due := shouldCompact(events[:DefaultMaxEvents-1], DefaultMaxEvents, DefaultMaxAge, now); due {
		t.Errorf("499 events with recent ts: want not due, got (%q,%v)", reason, due)
	}

	// --- age branch, at the real 90-day default ---
	oldTS := now.Add(-91 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	oldEvents := []*event.Envelope{
		{V: 1, Kind: "chat.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t61", TS: oldTS,
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t61", Data: map[string]any{}, Extra: map[string]any{}},
	}
	if reason, due := shouldCompact(oldEvents, DefaultMaxEvents, DefaultMaxAge, now); !due || reason != "age" {
		t.Errorf("91-day-old baseline: want (age,true), got (%q,%v)", reason, due)
	}
	recentTS := now.Add(-89 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	recentEvents := []*event.Envelope{
		{V: 1, Kind: "chat.post", ID: "01j8xq4d3nbz9k7w2m5e8h1t61", TS: recentTS,
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t61", Data: map[string]any{}, Extra: map[string]any{}},
	}
	if reason, due := shouldCompact(recentEvents, DefaultMaxEvents, DefaultMaxAge, now); due {
		t.Errorf("89-day-old baseline: want not due, got (%q,%v)", reason, due)
	}

	// --- no new events since last checkpoint: never due ---
	cpOnly := []*event.Envelope{
		{V: 1, Kind: "chat.checkpoint", ID: "01j8xq4d3nbz9k7w2m5e8h1t99", TS: oldTS,
			Actor: "a@example.com", Entity: "01j8xq4d3nbz9k7w2m5e8h1t99", Data: map[string]any{}, Extra: map[string]any{}},
	}
	if reason, due := shouldCompact(cpOnly, 1, time.Nanosecond, now); due {
		t.Errorf("checkpoint with no following events: want not due, got (%q,%v)", reason, due)
	}
}

// foldStateSignature renders a fold state (including Scratch) as canonical
// JSON, for comparing two folds byte for byte.
func foldStateSignature(t *testing.T, s *materialize.State) string {
	t.Helper()
	m, err := materialize.StateToMap(s)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := event.EncodeString(m)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}
