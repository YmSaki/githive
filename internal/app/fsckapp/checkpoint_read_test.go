package fsckapp

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// fullWalkState is the pre-shortcut answer: read every event on ref and fold
// it from empty.
func fullWalkState(t *testing.T, dir, ref string, registry *materialize.Registry) *materialize.State {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := gitx.New(dir).RevParse(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if oid == "" {
		t.Fatalf("ref %s does not exist", ref)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	return registry.Fold(events)
}

func assertCheckpointExists(t *testing.T, report *Report, ref string) {
	t.Helper()
	for _, cp := range report.Checkpoints {
		if cp.Ref == ref {
			return
		}
	}
	t.Fatalf("expected a checkpoint on %s, got %+v", ref, report.Checkpoints)
}

// TestNotifyStateSurvivesCheckpoint is the notify half of A3's Scratch
// requirement: acks live only in State.Scratch, so a checkpoint that a
// reader restarts from must carry them. The assertion is made after writing
// more events past the checkpoint, which is when the shortcut actually
// engages.
func TestNotifyStateSurvivesCheckpoint(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := notifyapp.New(dir)

	first, err := svc.Post(ctx, []string{"user:a@example.com"}, "p1", "b1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Post(ctx, []string{"user:b@example.com"}, "p2", "b2", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Ack(ctx, []string{first}); err != nil {
		t.Fatal(err)
	}

	ref := string(refspace.NotifyStreamRef)
	before := materialize.NotifyAcks(fullWalkState(t, dir, ref, materialize.NotifyRegistry))
	if len(before) == 0 {
		t.Fatal("fixture produced no acks; the test would prove nothing")
	}

	report, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointExists(t, report, ref)

	// A write after the checkpoint goes through the shortcut path.
	if _, err := svc.Post(ctx, []string{"user:c@example.com"}, "p3", "b3", nil, ""); err != nil {
		t.Fatal(err)
	}

	full := fullWalkState(t, dir, ref, materialize.NotifyRegistry)
	after := materialize.NotifyAcks(full)
	if len(after) != len(before) {
		t.Fatalf("acks changed across the checkpoint: %v -> %v", before, after)
	}

	// The tree the last write committed must equal what a full walk renders.
	assertTreeMatchesFullWalk(t, dir, ref, materialize.NotifyRegistry, notifyapp.TreeFiles)
}

// TestUsersStateSurvivesCheckpoint is the users half: policy lives in
// Scratch, and users is also the one feature whose State.Meta stays nil, so
// this covers the null-meta round-trip too.
func TestUsersStateSurvivesCheckpoint(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	ctx := context.Background()
	svc := usersapp.New(dir)

	if err := svc.AddUser(ctx, "alice", "Alice", "a@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PolicySet(ctx, []any{map[string]any{"ref": "refs/projects/*", "allow": "write"}}, "deny"); err != nil {
		t.Fatal(err)
	}

	ref := usersapp.RegistryRef
	before := materialize.Policy(fullWalkState(t, dir, ref, materialize.UsersRegistry))
	if before == nil {
		t.Fatal("fixture produced no policy; the test would prove nothing")
	}

	report, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointExists(t, report, ref)

	if err := svc.AddUser(ctx, "bob", "Bob", "b@example.com", "human", nil); err != nil {
		t.Fatal(err)
	}

	full := fullWalkState(t, dir, ref, materialize.UsersRegistry)
	if materialize.Policy(full) == nil {
		t.Fatal("policy was lost across the checkpoint")
	}
	if full.Meta != nil {
		t.Error("users state grew a non-nil Meta across the checkpoint")
	}
	assertTreeMatchesFullWalk(t, dir, ref, materialize.UsersRegistry, usersapp.TreeFiles)
}

// assertTreeMatchesFullWalk compares the tree the ref's head commit actually
// holds against the tree a from-scratch fold renders. This is the invariant
// the shortcut must not break: a clone that took the shortcut and a clone
// that walked everything materialize byte-identical trees
// (docs/02-data-model.md「決定性の不変条件」).
func assertTreeMatchesFullWalk(t *testing.T, dir, ref string, registry *materialize.Registry, treeFiles func(*materialize.State) (map[string][]byte, error)) {
	t.Helper()
	want, err := treeFiles(fullWalkState(t, dir, ref, registry))
	if err != nil {
		t.Fatal(err)
	}
	r := gitx.New(dir)
	ctx := context.Background()
	for path, wantBody := range want {
		got, err := r.Show(ctx, ref, path)
		if err != nil {
			t.Errorf("%s:%s missing from the committed tree: %v", ref, path, err)
			continue
		}
		if string(got) != string(wantBody) {
			t.Errorf("%s:%s differs from a full-walk fold:\n--- committed ---\n%s\n--- full walk ---\n%s",
				ref, path, got, wantBody)
		}
	}
}

// TestCompactSingletonStreams closes issue #58: the singleton-stream branch
// of compaction (notify/users, featureWriter.perEntity == false) had no
// end-to-end coverage, even though it is the branch where the checkpoint's
// entity is the checkpoint's own event id rather than the chain's id
// (ADR-0013「シングルトンストリーム」).
func TestCompactSingletonStreams(t *testing.T) {
	requireGit(t)
	cases := []struct {
		name     string
		ref      string
		registry *materialize.Registry
		seed     func(t *testing.T, dir string)
	}{
		{
			name:     "notify",
			ref:      notifyapp.StreamRef,
			registry: materialize.NotifyRegistry,
			seed: func(t *testing.T, dir string) {
				svc := notifyapp.New(dir)
				ctx := context.Background()
				id, err := svc.Post(ctx, []string{"user:a@example.com"}, "p1", "b1", nil, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.Post(ctx, []string{"user:b@example.com"}, "p2", "b2", nil, ""); err != nil {
					t.Fatal(err)
				}
				if err := svc.Ack(ctx, []string{id}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "users",
			ref:      usersapp.RegistryRef,
			registry: materialize.UsersRegistry,
			seed: func(t *testing.T, dir string) {
				svc := usersapp.New(dir)
				ctx := context.Background()
				if err := svc.AddUser(ctx, "alice", "Alice", "a@example.com", "human", []string{"admin"}); err != nil {
					t.Fatal(err)
				}
				if err := svc.GroupSet(ctx, "reviewers", []string{"alice"}, "reviewers"); err != nil {
					t.Fatal(err)
				}
				if err := svc.PolicySet(ctx, []any{map[string]any{"ref": "refs/projects/*", "allow": "write"}}, "deny"); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newTestRepo(t)
			ctx := context.Background()
			c.seed(t, dir)

			before := foldStateSignature(t, fullWalkState(t, dir, c.ref, c.registry))

			report, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
			if err != nil {
				t.Fatal(err)
			}

			var cp *CheckpointInfo
			for i := range report.Checkpoints {
				if report.Checkpoints[i].Ref == c.ref {
					cp = &report.Checkpoints[i]
				}
			}
			if cp == nil {
				t.Fatalf("expected a checkpoint on %s, got %+v", c.ref, report.Checkpoints)
			}
			// The singleton convention: entity is the checkpoint's own event
			// id, since the stream has no chain-level entity (ADR-0013).
			if cp.Entity != cp.EventID {
				t.Errorf("entity = %q, want it to equal the checkpoint's own event id %q", cp.Entity, cp.EventID)
			}
			if cp.Feature != c.name {
				t.Errorf("feature = %q, want %q", cp.Feature, c.name)
			}

			after := foldStateSignature(t, fullWalkState(t, dir, c.ref, c.registry))
			if after != before {
				t.Errorf("the checkpoint changed the fold result:\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}

			// Re-running must not append a second checkpoint: there are no
			// new events since the first one.
			again, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, c2 := range again.Checkpoints {
				if c2.Ref == c.ref {
					t.Errorf("a second checkpoint was appended with no new events: %+v", c2)
				}
			}
		})
	}
}
