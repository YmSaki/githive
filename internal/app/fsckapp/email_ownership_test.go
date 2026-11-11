package fsckapp

import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestRawRegistryDuplicateEmailIsExplicitInFsckAndVerify(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	if err := usersapp.New(dir).AddUser(ctx, "alice", "", "shared@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, refspace.UsersRegistryRef)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	id, ts := idgen.NewWithTimestamp()
	env := &event.Envelope{V: event.SchemaVersion, ID: id, TS: ts, Kind: "users.user_set", Actor: "tester@example.com", Entity: id, Data: map[string]any{"username": "robot", "fields": map[string]any{"emails": []any{"shared@example.com"}, "kind": "agent"}}}
	files, err := usersapp.TreeFiles(materialize.UsersRegistry.Fold(append(events, env)))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := chain.AppendEvent(repo, plumbing.NewHash(oid), env, "peer registration", files, chain.Signature{Name: "Tester", Email: env.Actor, When: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, refspace.UsersRegistryRef, hash.String(), oid); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsWithCode(report, "ambiguous_email"); len(got) != 1 {
		t.Fatalf("fsck missing conflict: %+v", report)
	}
	verified, err := verifyapp.VerifyRef(ctx, dir, refspace.UsersRegistryRef, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range verified.Issues {
		if issue.Code == "ambiguous_email" {
			found = true
		}
	}
	if !found || verified.OK() {
		t.Fatalf("verify missing conflict: %+v", verified)
	}
}
