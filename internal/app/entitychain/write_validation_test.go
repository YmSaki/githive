package entitychain

import (
	"context"
	"errors"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func TestWriterRejectsInvalidDataBeforeRenderingOrAppending(t *testing.T) {
	w := testWriter(newTestRepo(t))
	appendEvent(t, w, "issue.create", map[string]any{"title": "valid"})
	_, before, err := w.CurrentEvents(context.Background(), testEntity)
	if err != nil {
		t.Fatal(err)
	}
	w.TreeFiles = func(*materialize.State) (map[string][]byte, error) {
		t.Fatal("invalid input reached the tree renderer")
		return nil, nil
	}
	_, err = w.Append(context.Background(), testEntity, func() (*event.Envelope, string) {
		return &event.Envelope{V: event.SchemaVersion, Kind: "issue.comment", ID: nextTestID(t),
			TS: "2026-07-28T09:00:00.000Z", Entity: testEntity,
			Data: map[string]any{"body": "comment", "supersedes": "x\nauthor: forged"}}, "comment"
	})
	var invalid *event.InvalidInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected typed invalid input, got %v", err)
	}
	_, after, err := w.CurrentEvents(context.Background(), testEntity)
	if err != nil || after != before {
		t.Fatalf("invalid input changed ref: %s -> %s (%v)", before, after, err)
	}
}
