package syncapp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/oklog/ulid/v2"
	"github.com/ymsaki/githive/internal/app/fsckapp"
	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/sign"
)

func TestRawUsersKeyDependencyControlsRegistryAdmission(t *testing.T) {
	requireSSHSigning(t)
	ctx := context.Background()
	const admin = "admin@example.com"
	adminKey := genSigningKey(t, admin)
	targetKey := genSigningKey(t, "target@example.com")
	readPub := func(path string) string {
		raw, err := os.ReadFile(path + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(raw))
	}
	adminPub, targetPub := readPub(adminKey), readPub(targetKey)
	for _, valid := range []bool{true, false} {
		name := "ignored-revocation"
		if valid {
			name = "valid-idempotent-revocations"
		}
		t.Run(name, func(t *testing.T) {
			origin := initBareOrigin(t)
			a := cloneRepo(t, origin, admin)
			configureSigning(t, a, adminKey)
			repo, err := chain.OpenRepository(a)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := identity.Resolve(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			signer, cleanup, err := sign.ResolveSigner(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			baseTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
			envelope := func(offset int, kind string, data map[string]any) *event.Envelope {
				when := baseTime.Add(time.Duration(offset) * time.Millisecond)
				id := strings.ToLower(ulid.MustNew(ulid.Timestamp(when), bytes.NewReader(make([]byte, 10))).String())
				return &event.Envelope{V: event.SchemaVersion, Kind: kind, ID: id, Entity: id, Actor: admin,
					TS: when.Format("2006-01-02T15:04:05.000Z"), Data: data}
			}
			var head plumbing.Hash
			var history []*event.Envelope
			appendRaw := func(env *event.Envelope) {
				t.Helper()
				history = append(history, env)
				files, err := usersapp.TreeFiles(materialize.UsersRegistry.Fold(history))
				if err != nil {
					t.Fatal(err)
				}
				head, err = chain.AppendEvent(repo, head, env, env.Kind, files, sig, chain.WithSigner(signer))
				if err != nil {
					t.Fatal(err)
				}
			}
			appendRaw(envelope(0, "users.user_set", map[string]any{"username": "alice", "fields": map[string]any{
				"emails": []any{admin}, "roles": []any{"admin"}, "kind": "human", "status": "active"}}))
			appendRaw(envelope(10, "users.key_add", map[string]any{"username": "alice", "pub": adminPub}))
			appendRaw(envelope(30, "users.key_add", map[string]any{"username": "alice", "pub": targetPub}))
			good := head.String()
			r := gitx.New(a)
			if err := r.UpdateRef(ctx, registryRef, good, gitx.ZeroOID); err != nil {
				t.Fatal(err)
			}
			publish := func() {
				t.Helper()
				if out, err := exec.Command("git", "-C", a, "push", "origin", registryRef+":"+registryRef).CombinedOutput(); err != nil {
					t.Fatalf("raw push: %v\n%s", err, out)
				}
			}
			publish()
			b := cloneRepo(t, origin, "b@example.com")
			if _, err := Sync(ctx, b, "origin", []string{registryRef}, 0); err != nil {
				t.Fatal(err)
			}
			offset := 20 // sorts before target key_add; unchanged fold ignores this revoke
			if valid {
				offset = 40
			}
			revoke := envelope(offset, "users.key_revoke", map[string]any{"username": "alice", "pub": targetPub})
			appendRaw(revoke)
			if valid {
				appendRaw(revoke) // identical canonical event copy in another commit
				appendRaw(envelope(50, "users.key_revoke", map[string]any{"username": "alice", "pub": targetPub}))
			}
			if err := r.UpdateRef(ctx, registryRef, head.String(), good); err != nil {
				t.Fatal(err)
			}
			// The reducer remains unchanged: valid repeated revokes leave only
			// the signing key active; the invalid fixture still ignores its
			// target revoke and must be caught by admission instead.
			active := materialize.ActiveKeysForEmail(materialize.UsersRegistry.Fold(history), admin)
			wantActive := 2
			if valid {
				wantActive = 1
			}
			if len(active) != wantActive || slices.Contains(active, targetPub) != !valid {
				t.Fatalf("unchanged fold's key state differs: valid=%t active=%v", valid, active)
			}
			verified, err := verifyapp.VerifyRef(ctx, a, registryRef, "")
			if err != nil {
				t.Fatal(err)
			}
			if valid && !verified.OK() {
				t.Fatalf("valid signed idempotent history rejected: %+v", verified)
			}
			if !valid && verified.OK() {
				t.Fatal("fully signed registry falsely verified although its revoke sorts before key_add and leaves the target key active")
			}
			if !valid && (len(verified.Issues) != 1 || verified.Issues[0].Code != "event_order_violation") {
				t.Fatalf("wrong diagnostic: %+v", verified)
			}
			_, _, _, readErr := usersapp.New(a).List(ctx)
			if valid && readErr != nil || !valid && !errors.Is(readErr, event.ErrEventOrderViolation) {
				t.Fatalf("read validity=%t error=%v", valid, readErr)
			}
			fsck, err := fsckapp.New(a).Run(ctx, fsckapp.Options{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range fsck.Findings {
				found = found || finding.Code == "event_order_violation"
			}
			if found == valid {
				t.Fatalf("fsck validity=%t findings=%+v", valid, fsck.Findings)
			}
			publish()
			_, syncErr := Sync(ctx, b, "origin", []string{registryRef}, 0)
			if valid && syncErr != nil || !valid && !errors.Is(syncErr, event.ErrEventOrderViolation) {
				t.Fatalf("sync validity=%t error=%v", valid, syncErr)
			}
			want := head.String()
			if !valid {
				want = good
			}
			if got, err := gitx.New(b).RevParse(ctx, registryRef); err != nil || got != want {
				t.Fatalf("sync adopted invalid registry: %q want %q %v", got, want, err)
			}
			c := cloneRepo(t, origin, "c@example.com")
			_, initErr := initapp.Init(ctx, c, "origin", "users-order")
			if valid && initErr != nil || !valid && !errors.Is(initErr, event.ErrEventOrderViolation) {
				t.Fatalf("init validity=%t error=%v", valid, initErr)
			}
			want = head.String()
			if !valid {
				want = ""
			}
			if got, err := gitx.New(c).RevParse(ctx, registryRef); err != nil || got != want {
				t.Fatalf("init adopted invalid registry: %q want %q %v", got, want, err)
			}
			if !valid {
				id, err := issueapp.New(a).NewIssue(ctx, "signed with a poisoned registry", "", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				report, err := verifyapp.VerifyRef(ctx, a, "refs/projects/issue/"+id, "")
				detected := errors.Is(err, event.ErrEventOrderViolation)
				if report != nil {
					for _, issue := range report.Issues {
						detected = detected || issue.Code == "event_order_violation" || strings.Contains(issue.Message, "event_order_violation")
					}
				}
				if !detected || err == nil && report != nil && report.OK() {
					t.Fatalf("another signed ref trusted ignored revocation: report=%+v err=%v", report, err)
				}
			}
		})
	}
}
