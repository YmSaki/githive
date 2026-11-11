package entitychain

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// A checkpoint is advisory data, including when its shape looks valid. A
// normal read or subsequent signed write must derive state from real events.
func TestUntrustedCheckpointFallsBackBeforeReadAndWrite(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(map[string]any)
	}{
		{"forged-meta", func(m map[string]any) { m["meta"].(map[string]any)["title"] = "FORGED" }},
		{"missing-collections", func(m map[string]any) { m["collections"] = map[string]any{} }},
		{"unsafe-comment-key", func(m map[string]any) {
			m["collections"].(map[string]any)["comments"].(map[string]any)["x/../../pwn"] = map[string]any{"body": "forged"}
		}},
		{"missing-scratch-state", func(m map[string]any) { m["scratch"] = map[string]any{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newTestRepo(t)
			w := testWriter(dir)
			seedChain(t, w)
			events, oid, err := w.CurrentEvents(context.Background(), testEntity)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := materialize.StateToMap(w.Registry.Fold(events))
			if err != nil {
				t.Fatal(err)
			}
			tc.tamper(raw)
			maxID := ""
			for _, e := range events {
				if e.ID > maxID {
					maxID = e.ID
				}
			}
			appendEvent(t, w, "issue.checkpoint", map[string]any{"state": raw, "head": oid, "max_event_id": maxID, "event_count": len(events)})
			base, _, _, err := w.foldInputs(context.Background(), testEntity, "")
			if err != nil {
				t.Fatal(err)
			}
			if base != nil {
				t.Fatal("untrusted snapshot was accepted as fold input")
			}
			got, err := w.Fold(context.Background(), testEntity)
			if err != nil {
				t.Fatal(err)
			}
			if treeBytes(t, got) != treeBytes(t, fullFold(t, w)) {
				t.Fatal("read used forged checkpoint state")
			}
			appendEvent(t, w, "issue.comment", map[string]any{"body": "legitimate comment"})
			got, err = w.Fold(context.Background(), testEntity)
			if err != nil {
				t.Fatal(err)
			}
			if treeBytes(t, got) != treeBytes(t, fullFold(t, w)) {
				t.Fatal("write preserved forged checkpoint state")
			}
			_, head, err := w.CurrentEvents(context.Background(), testEntity)
			if err != nil {
				t.Fatal(err)
			}
			repo, err := chain.OpenRepository(dir)
			if err != nil {
				t.Fatal(err)
			}
			files, err := chain.ReadTree(repo, plumbing.NewHash(head))
			if err != nil {
				t.Fatal(err)
			}
			if string(files["state.json"]) != treeBytes(t, fullFold(t, w)) {
				t.Fatal("committed tree contains state not justified by real events")
			}
		})
	}
}

func TestCheckpointRejectsUnverifiedOrderingAndOlderSemantics(t *testing.T) {
	for _, oldVersion := range []bool{false, true} {
		name := "forged-max-event-id"
		if oldVersion {
			name = "older-envelope-version"
		}
		t.Run(name, func(t *testing.T) {
			dir := newTestRepo(t)
			w := testWriter(dir)
			seedChain(t, w)
			events, oid, err := w.CurrentEvents(context.Background(), testEntity)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := materialize.StateToMap(w.Registry.Fold(events))
			if err != nil {
				t.Fatal(err)
			}
			maxID := ""
			for _, e := range events {
				if e.ID > maxID {
					maxID = e.ID
				}
			}
			version := event.SchemaVersion
			if oldVersion {
				version = event.MinSchemaVersion
			} else {
				maxID = "00000000000000000000000001"
			}
			_, err = w.Append(context.Background(), testEntity, func() (*event.Envelope, string) {
				return &event.Envelope{V: version, Kind: "issue.checkpoint", ID: nextTestID(t), TS: "2026-07-28T09:00:00.000Z", Entity: testEntity, Data: map[string]any{"state": raw, "head": oid, "max_event_id": maxID, "event_count": len(events)}, Extra: map[string]any{}}, "issue.checkpoint"
			})
			if err != nil {
				t.Fatal(err)
			}
			base, _, _, err := w.foldInputs(context.Background(), testEntity, "")
			if err != nil {
				t.Fatal(err)
			}
			if base != nil {
				t.Fatal("checkpoint with unverified ordering/semantics was accepted")
			}
		})
	}
}
