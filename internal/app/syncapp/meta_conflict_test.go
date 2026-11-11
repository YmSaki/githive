package syncapp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func metaConflictFixture(t *testing.T, dir, project string, version int, compact bool) string {
	t.Helper()
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, ts := idgen.NewWithTimestamp()
	data := map[string]any{"schema_version": version, "project": project, "features": []any{"issue"}, "created_at": "2026-11-11T00:00:00.000Z"}
	raw, err := event.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	if compact {
		text, err := event.EncodeCompactLine(data)
		if err != nil {
			t.Fatal(err)
		}
		raw = []byte(text)
	}
	env := &event.Envelope{V: event.SchemaVersion, Kind: "meta.init", ID: id, TS: ts, Actor: "audit@example.com", Entity: id, Data: data}
	sig := object.Signature{Name: "Audit", Email: "audit@example.com", When: time.Now()}
	hash, err := chain.AppendEvent(repo, chain.ZeroHash, env, "init", map[string][]byte{metaConfigPath: raw}, sig)
	if err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

func TestMetaMergeEqualVersionConflictsInBothDirections(t *testing.T) {
	dir := cloneRepo(t, initBareOrigin(t), "audit@example.com")
	a, b := metaConflictFixture(t, dir, "a", 1, false), metaConflictFixture(t, dir, "b", 1, false)
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		_, err := metaMergeFiles(context.Background(), dir, pair[0], pair[1])
		var conflict *MetaConfigConflictError
		if !errors.Is(err, ErrMetaConfigConflict) || !errors.As(err, &conflict) {
			t.Fatalf("expected explicit conflict: %v", err)
		}
		if conflict.LocalOID != pair[0] || conflict.RemoteOID != pair[1] {
			t.Fatalf("wrong inspection OIDs: %+v", conflict)
		}
	}
}

func TestMetaMergeIdenticalValuesHaveSameCanonicalBytes(t *testing.T) {
	dir := cloneRepo(t, initBareOrigin(t), "audit@example.com")
	a, b := metaConflictFixture(t, dir, "same", 1, false), metaConflictFixture(t, dir, "same", 1, true)
	ab, err := metaMergeFiles(context.Background(), dir, a, b)
	if err != nil {
		t.Fatal(err)
	}
	ba, err := metaMergeFiles(context.Background(), dir, b, a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab[metaConfigPath], ba[metaConfigPath]) {
		t.Fatal("identical config values depend on merge direction")
	}
}

func TestMetaMergeHigherSchemaVersionStillWins(t *testing.T) {
	dir := cloneRepo(t, initBareOrigin(t), "audit@example.com")
	a, b := metaConflictFixture(t, dir, "old", 1, false), metaConflictFixture(t, dir, "new", 2, false)
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		files, err := metaMergeFiles(context.Background(), dir, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(files[metaConfigPath]), `"project": "new"`) {
			t.Fatalf("higher schema was discarded: %s", files[metaConfigPath])
		}
	}
}

func TestSyncMetaConflictPreservesBothRefsAndHistories(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "a-to-b"
		if reverse {
			name = "b-to-a"
		}
		t.Run(name, func(t *testing.T) {
			origin := initBareOrigin(t)
			dir := cloneRepo(t, origin, "audit@example.com")
			ctx := context.Background()
			r := gitx.New(dir)
			a, b := metaConflictFixture(t, dir, "a", 1, false), metaConflictFixture(t, dir, "b", 1, false)
			local, remote := a, b
			if reverse {
				local, remote = b, a
			}
			if _, err := r.Push(ctx, "origin", remote+":"+refspace.MetaConfigRef); err != nil {
				t.Fatal(err)
			}
			if err := r.UpdateRef(ctx, refspace.MetaConfigRef, local, gitx.ZeroOID); err != nil {
				t.Fatal(err)
			}
			_, err := Sync(ctx, dir, "origin", []string{refspace.MetaConfigRef}, 0)
			if !errors.Is(err, ErrMetaConfigConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			after, err := r.RevParse(ctx, refspace.MetaConfigRef)
			if err != nil {
				t.Fatal(err)
			}
			if after != local {
				t.Fatal("conflict advanced local ref")
			}
			remoteAfter, err := gitx.New(origin).RevParse(ctx, refspace.MetaConfigRef)
			if err != nil {
				t.Fatal(err)
			}
			if remoteAfter != remote {
				t.Fatal("conflict changed remote ref")
			}
			tracking, _ := refspace.RemoteTrackingRef(refspace.MetaConfigRef)
			fetched, err := r.RevParse(ctx, tracking)
			if err != nil {
				t.Fatal(err)
			}
			if fetched != remote {
				t.Fatal("remote history was not retained for inspection")
			}
		})
	}
}
