package fsckapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/ymsaki/githive/internal/app/initapp"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestForeignEventContractsReportedAndNotAdopted(t *testing.T) {
	for _, tc := range []struct {
		name, kind, feature  string
		data                 map[string]any
		wrongEntity, compact bool
		code                 string
	}{
		{"missing-body", "chat.post", "chat", map[string]any{}, false, false, "invalid_event_data"},
		{"wrong-title-type", "issue.edit", "issue", map[string]any{"title": 123}, false, false, "invalid_event_data"},
		{"wrong-status", "issue.status", "issue", map[string]any{"to": "bogus"}, false, false, "invalid_event_data"},
		{"wrong-feature", "issue.create", "task", map[string]any{"title": "valid"}, false, false, "event_ref_mismatch"},
		{"wrong-entity", "issue.create", "issue", map[string]any{"title": "valid"}, true, false, "event_ref_mismatch"},
		{"non-canonical", "issue.create", "issue", map[string]any{"title": "valid"}, false, true, "non_canonical_event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newTestRepo(t)
			ctx := context.Background()
			id, ts := idgen.NewWithTimestamp()
			entity := id
			if tc.wrongEntity {
				entity = idgen.New()
			}
			env := &event.Envelope{V: event.SchemaVersion, Kind: tc.kind, ID: id, TS: ts, Actor: "tester@example.com", Entity: entity, Data: tc.data}
			repo, err := chain.OpenRepository(dir)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			if tc.kind == "issue.create" {
				files, err = issueapp.TreeFiles(materialize.IssueRegistry.Fold([]*event.Envelope{env}))
				if err != nil {
					t.Fatal(err)
				}
			}
			hash, err := chain.AppendEvent(repo, chain.ZeroHash, env, "foreign fixture", files, chain.Signature{Name: "Tester", Email: env.Actor, When: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if tc.compact {
				c, err := object.GetCommit(repo.Storer, hash)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := env.CanonicalJSON()
				if err != nil {
					t.Fatal(err)
				}
				m, err := event.DecodeGeneric(raw)
				if err != nil {
					t.Fatal(err)
				}
				line, err := event.EncodeCompactLine(m)
				if err != nil {
					t.Fatal(err)
				}
				c.Message = "foreign fixture\n\n" + line + "\n"
				obj := repo.Storer.NewEncodedObject()
				if err := c.Encode(obj); err != nil {
					t.Fatal(err)
				}
				hash, err = repo.Storer.SetEncodedObject(obj)
				if err != nil {
					t.Fatal(err)
				}
			}
			ref := "refs/projects/" + tc.feature + "/" + id
			r := gitx.New(dir)
			if err := r.UpdateRef(ctx, ref, hash.String(), gitx.ZeroOID); err != nil {
				t.Fatal(err)
			}
			report, err := New(dir).Run(ctx, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got := findingsWithCode(report, tc.code); len(got) != 1 {
				t.Fatalf("missing contract diagnostic %s: %+v", tc.code, report)
			}
			compacted, err := New(dir).Run(ctx, Options{Compact: true, MaxEvents: 1})
			if err != nil || len(compacted.Checkpoints) != 0 {
				t.Fatalf("compact must preserve invalid history without re-signing: %+v %v", compacted, err)
			}
			if oid, err := r.RevParse(ctx, ref); err != nil || oid != hash.String() {
				t.Fatalf("compact changed invalid ref: %s %v", oid, err)
			}
			if tc.compact {
				return
			} // encoding is audited without rewriting/admission changes
			verified, err := verifyapp.VerifyRef(ctx, dir, ref, "")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range verified.Issues {
				found = found || issue.Code == tc.code
			}
			if !found {
				t.Fatalf("verify missing %s: %+v", tc.code, verified)
			}
			origin := t.TempDir()
			if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
				t.Fatalf("bare: %v %s", err, out)
			}
			if _, err := r.Push(ctx, origin, ref+":"+ref); err != nil {
				t.Fatal(err)
			}
			target := newTestRepo(t)
			if out, err := exec.Command("git", "-C", target, "remote", "add", "origin", origin).CombinedOutput(); err != nil {
				t.Fatalf("remote: %v %s", err, out)
			}
			_, err = syncapp.Sync(ctx, target, "origin", []string{ref}, 0)
			if err == nil {
				t.Fatal("sync adopted malformed foreign history")
			}
			if tc.code == "event_ref_mismatch" && !errors.Is(err, refspace.ErrEventRefMismatch) {
				t.Fatalf("wrong sync error: %v", err)
			}
			if oid, err := gitx.New(target).RevParse(ctx, ref); err != nil || oid != "" {
				t.Fatalf("rejected sync moved ref: %s %v", oid, err)
			}
			if _, err := initapp.Init(ctx, target, "origin", "project"); err == nil {
				t.Fatal("init adopted malformed foreign history")
			}
			if oid, err := gitx.New(target).RevParse(ctx, ref); err != nil || oid != "" {
				t.Fatalf("rejected init moved ref: %s %v", oid, err)
			}
		})
	}
}

func TestUnknownKindRemainsAForwardCompatibleWarning(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()
	id, err := issueapp.New(dir).NewIssue(ctx, "valid", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/projects/issue/" + id
	r := gitx.New(dir)
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := chain.ReadTree(repo, plumbing.NewHash(oid))
	if err != nil {
		t.Fatal(err)
	}
	eid, ts := idgen.NewWithTimestamp()
	env := &event.Envelope{V: event.SchemaVersion, Kind: "issue.future", ID: eid, TS: ts, Actor: "tester@example.com", Entity: id, Data: map[string]any{"opaque": []any{true, "kept"}}}
	hash, err := chain.AppendEvent(repo, plumbing.NewHash(oid), env, "future extension", files, chain.Signature{Name: "Tester", Email: env.Actor, When: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, ref, hash.String(), oid); err != nil {
		t.Fatal(err)
	}
	report, err := New(dir).Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsWithCode(report, "unknown_event_kind"); len(got) != 1 || got[0].Severity != SeverityWarning || !report.OK() {
		t.Fatalf("forwardkind not warning-only: %+v", report)
	}
	verified, err := verifyapp.VerifyRef(ctx, dir, ref, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Warnings) != 1 || verified.Warnings[0].Code != "unknown_event_kind" {
		t.Fatalf("verifier silently hid unknownkind: %+v", verified)
	}
	origin := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "--quiet", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare: %v %s", err, out)
	}
	if _, err := r.Push(ctx, origin, ref+":"+ref); err != nil {
		t.Fatal(err)
	}
	target := newTestRepo(t)
	if out, err := exec.Command("git", "-C", target, "remote", "add", "origin", origin).CombinedOutput(); err != nil {
		t.Fatalf("remote: %v %s", err, out)
	}
	if _, err := syncapp.Sync(ctx, target, "origin", []string{ref}, 0); err != nil {
		t.Fatalf("futurekind rejected: %v", err)
	}
}
