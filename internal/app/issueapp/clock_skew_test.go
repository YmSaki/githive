package issueapp

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestCommentRejectsClockBehindCreatingPeer(t *testing.T) {
	ctx := context.Background()
	source := newTestRepo(t).Dir
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	id := strings.ToLower(ulid.MustNew(ulid.Timestamp(future), bytes.NewReader(make([]byte, 10))).String())
	create := &event.Envelope{V: event.SchemaVersion, Kind: "issue.create", ID: id, Entity: id,
		TS: future.Format("2006-01-02T15:04:05.000Z"), Actor: "tester@example.com", Data: map[string]any{"title": "created by a future-clock peer"}}
	repo, err := chain.OpenRepository(source)
	if err != nil {
		t.Fatal(err)
	}
	files, err := TreeFiles(materialize.IssueRegistry.Fold([]*event.Envelope{create}))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := identity.Resolve(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	head, err := chain.AppendEvent(repo, chain.ZeroHash, create, "future create", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	if err := gitx.New(source).UpdateRef(ctx, ref, head.String(), gitx.ZeroOID); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", source, b).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"config", "user.email", "b@example.com"}, {"config", "user.name", "B"},
		{"fetch", "origin", "+" + ref + ":" + ref},
	} {
		if out, err := exec.Command("git", append([]string{"-C", b}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var skew *entitychain.ClockSkewError
	if err := New(b).Comment(ctx, id, "must not silently disappear", "", ""); !errors.As(err, &skew) {
		t.Fatalf("comment must report typed clock skew instead of silently dropping its body: %v", err)
	}
	if skew.LatestID != id || skew.LatestTimestamp != create.TS || !strings.Contains(skew.Recovery(), "no event was recorded") {
		t.Fatalf("clock-skew error lost recovery context: %+v", skew)
	}
	if after, err := gitx.New(b).RevParse(ctx, ref); err != nil || after != head.String() {
		t.Fatalf("ref changed after rejected comment: %s %v", after, err)
	}
}
