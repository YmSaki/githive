package notifyapp

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/apperr"
	"github.com/ymsaki/githive/internal/core/event"
)

func TestAckResolvesPostsBeforeWriting(t *testing.T) {
	svc := newTestRepo(t, "a@example.com")
	ctx := context.Background()
	ids := []string{"01j8xq4d3nbz9k7w2m5e8h1t61", "01j8xq4d3nbz9k7w2m5e8h1t62", "01j8xq4e3nbz9k7w2m5e8h1t63"}
	for _, id := range ids {
		_, err := svc.writer.Append(ctx, "", func() (*event.Envelope, string) {
			return &event.Envelope{V: event.SchemaVersion, Kind: "notify.post", ID: id, Entity: id,
				TS: "2026-07-04T00:00:00.000Z", Data: map[string]any{"targets": []any{"all"}, "title": id}}, "notification"
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	full, err := svc.AckIDs(ctx, []string{strings.ToUpper(ids[2][:8])})
	if err != nil || !reflect.DeepEqual(full, ids[2:]) {
		t.Fatalf("unique uppercase prefix: got %v, %v", full, err)
	}
	unread, err := svc.List(ctx, ListFilter{UnreadOnly: true, ActorEmail: "a@example.com"})
	if err != nil || len(unread) != 2 {
		t.Fatalf("short ack did not remove the post from unread: %v, %v", unread, err)
	}
	for _, invalid := range []string{"01j8xq4d", "01j8xq4f3nbz9k7w2m5e8h1t64", "../../etc/passwd", "short"} {
		t.Run(invalid, func(t *testing.T) {
			head := func() string {
				t.Helper()
				out, err := exec.Command("git", "-C", svc.Dir, "rev-parse", StreamRef).CombinedOutput()
				if err != nil {
					t.Fatal(err)
				}
				return string(out)
			}
			before := head()
			// A valid ID before the bad input must not be partially acked.
			err := svc.Ack(ctx, []string{ids[0], invalid})
			if invalid == "01j8xq4d" {
				var ambiguous *apperr.AmbiguousIDError
				if !errors.As(err, &ambiguous) || !reflect.DeepEqual(ambiguous.Candidates, ids[:2]) {
					t.Fatalf("expected sorted ambiguity candidates: %v", err)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected not found: %v", err)
			}
			if after := head(); after != before {
				t.Fatal("invalid batch appended an event")
			}
		})
	}
}
