package syncapp

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
)

func TestUnsafeRegistryNamesRejectReadsWritesAndIncomingSync(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		data       map[string]any
	}{
		{"user", "users.user_set", map[string]any{"username": "../escape", "fields": map[string]any{}}},
		{"group", "users.group_set", map[string]any{"name": "..", "members": []any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			origin := initBareOrigin(t)
			sender := cloneRepo(t, origin, "a@example.com")
			svc := usersapp.New(sender)
			if err := svc.AddUser(ctx, "alice", "Alice", "a@example.com", "human", []string{"admin"}); err != nil {
				t.Fatal(err)
			}
			if _, err := Sync(ctx, sender, "origin", []string{registryRef}, 0); err != nil {
				t.Fatal(err)
			}
			receiver := cloneRepo(t, origin, "b@example.com")
			if _, err := Sync(ctx, receiver, "origin", []string{registryRef}, 0); err != nil {
				t.Fatal(err)
			}
			before := refOID(t, receiver, registryRef)
			eid, ts := idgen.NewWithTimestamp()
			// Raw plumbing constructs corrupt remote history without app validation.
			bad := appendIdentityFixture(t, sender, registryRef, &event.Envelope{V: event.SchemaVersion, Kind: tc.kind, ID: eid, TS: ts, Actor: "a@example.com", Entity: eid, Data: tc.data})
			repo, err := chain.OpenRepository(sender)
			if err != nil {
				t.Fatal(err)
			}
			_, err = chain.WalkChain(repo, plumbing.NewHash(bad))
			var unsafe *event.UnsafeStoredDataError
			if !errors.As(err, &unsafe) {
				t.Fatalf("unsafe stored name reached fold: %v", err)
			}
			if _, _, _, err := svc.List(ctx); !errors.As(err, &unsafe) {
				t.Fatalf("registry read accepted unsafe history: %v", err)
			}
			if err := svc.AddUser(ctx, "carol", "", "carol@example.com", "human", nil); !errors.As(err, &unsafe) {
				t.Fatalf("write accepted unsafe history: %v", err)
			}
			if refOID(t, sender, registryRef) != bad {
				t.Fatal("failed app write moved ref")
			}
			pushed, err := gitx.New(sender).Push(ctx, "origin", registryRef+":"+registryRef)
			if err != nil || len(pushed) != 1 || !pushed[0].OK {
				t.Fatalf("raw fixture push: %v %v", pushed, err)
			}
			if _, err := Sync(ctx, receiver, "origin", []string{registryRef}, 0); !errors.As(err, &unsafe) {
				t.Fatalf("incoming fast-forward adopted unsafe registry: %v", err)
			}
			if refOID(t, receiver, registryRef) != before {
				t.Fatal("failed incoming sync advanced local registry")
			}
			fresh := cloneRepo(t, origin, "fresh@example.com")
			if _, err := Sync(ctx, fresh, "origin", []string{registryRef}, 0); !errors.As(err, &unsafe) {
				t.Fatalf("first sync adopted unsafe registry: %v", err)
			}
			if refOID(t, fresh, registryRef) != "" {
				t.Fatal("failed first sync installed registry")
			}
			bootstrap := cloneRepo(t, origin, "bootstrap@example.com")
			if _, err := initapp.Init(ctx, bootstrap, "origin", "unsafe registry"); !errors.As(err, &unsafe) {
				t.Fatalf("init adopted unsafe registry: %v", err)
			}
			if refOID(t, bootstrap, registryRef) != "" {
				t.Fatal("failed init installed registry")
			}
		})
	}
}
