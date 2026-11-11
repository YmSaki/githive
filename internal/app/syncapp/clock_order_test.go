package syncapp

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/oklog/ulid/v2"
	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestIncomingPreCreateEventIsRejectedAndReported(t *testing.T) {
	ctx := context.Background()
	origin := initBareOrigin(t)
	a := cloneRepo(t, origin, "a@example.com")
	b := cloneRepo(t, origin, "b@example.com")
	ref := seedIssues(t, a, 1)[0]
	for _, dir := range []string{a, b} {
		if _, err := Sync(ctx, dir, "origin", []string{ref}, 0); err != nil {
			t.Fatal(err)
		}
	}
	r := gitx.New(a)
	good, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(a)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(good))
	if err != nil {
		t.Fatal(err)
	}
	older := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	legacy := &event.Envelope{V: event.SchemaVersion, Kind: "issue.comment",
		ID: strings.ToLower(ulid.MustNew(ulid.Timestamp(older), bytes.NewReader(make([]byte, 10))).String()),
		TS: older.Format("2006-01-02T15:04:05.000Z"), Actor: "a@example.com", Entity: strings.TrimPrefix(ref, "refs/projects/issue/"),
		Data: map[string]any{"body": "legacy text that the reducer would silently drop"}}
	files, err := issueapp.TreeFiles(materialize.IssueRegistry.Fold(append(events, legacy)))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := identity.Resolve(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := chain.AppendEvent(repo, plumbing.NewHash(good), legacy, "legacy skewed comment", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, ref, bad.String(), good); err != nil {
		t.Fatal(err)
	}
	// An old or external writer bypasses current admission and publishes.
	if out, err := exec.Command("git", "-C", a, "push", "origin", ref+":"+ref).CombinedOutput(); err != nil {
		t.Fatalf("legacy push: %v\n%s", err, out)
	}
	if _, err := Sync(ctx, b, "origin", []string{ref}, 0); !errors.Is(err, event.ErrEventOrderViolation) {
		t.Fatalf("sync silently accepted pre-create body loss: %v", err)
	}
	if local, err := gitx.New(b).RevParse(ctx, ref); err != nil || local != good {
		t.Fatalf("failed sync changed good local ref: %q %v", local, err)
	}
	// Inspect the legacy history locally without changing its original bytes.
	if err := gitx.New(b).UpdateRef(ctx, ref, bad.String(), good); err != nil {
		t.Fatal(err)
	}
	if _, err := issueapp.New(b).Show(ctx, legacy.Entity); !errors.Is(err, event.ErrEventOrderViolation) {
		t.Fatalf("read silently hid pre-create body: %v", err)
	}
	fsck, err := fsckapp.New(b).Run(ctx, fsckapp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range fsck.Findings {
		found = found || finding.Ref == ref && finding.Code == "event_order_violation"
	}
	if !found {
		t.Fatalf("fsck failed to report invisible legacy event: %+v", fsck)
	}
	verified, err := verifyapp.VerifyRef(ctx, b, ref, "")
	if err != nil || len(verified.Issues) == 0 || verified.Issues[0].Code != "event_order_violation" {
		t.Fatalf("verify failed to report legacy order: %+v %v", verified, err)
	}
}
