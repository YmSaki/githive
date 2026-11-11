package usersapp

import (
	"fmt"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// Remote envelopes bypass the local CLI's username checks. The object writer
// must still reject unsafe rendered paths without changing fold semantics.
func TestRegistryTreeRejectsUnsafeRemoteNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   string
		field  string
		values []string
	}{
		{"user traversal", "users.user_set", "username", []string{"a/../../b"}},
		{"group traversal", "users.group_set", "name", []string{"a/../../b"}},
		{"user collision", "users.user_set", "username", []string{"foo", "foo.json/bar"}},
		{"git directory", "users.user_set", "username", []string{".git/config"}},
		{"NUL", "users.user_set", "username", []string{"bad\x00name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []*event.Envelope
			for i, name := range tc.values {
				events = append(events, &event.Envelope{
					V: event.SchemaVersion, ID: fmt.Sprintf("%026d", i+1), Kind: tc.kind,
					Data: map[string]any{tc.field: name},
				})
			}
			files, err := TreeFiles(materialize.UsersRegistry.Fold(events))
			if err != nil {
				t.Fatal(err)
			}
			storage := memory.NewStorage()
			repo, err := git.Init(storage, nil)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := chain.WriteTree(repo, files)
			if err == nil || hash != plumbing.ZeroHash {
				t.Fatalf("unsafe remote registry accepted: hash=%s err=%v", hash, err)
			}
			if len(storage.Objects) != 0 {
				t.Fatalf("unsafe registry wrote %d objects", len(storage.Objects))
			}
		})
	}
}
