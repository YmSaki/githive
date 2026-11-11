package notifyapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func TestPostRejectsUndeliverableTargetsAndInvalidSources(t *testing.T) {
	svc := newTestRepo(t, "a@example.com")
	refs := func() string {
		t.Helper()
		b, err := exec.Command("git", "-C", svc.Dir, "for-each-ref", "--format=%(refname) %(objectname)").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := refs()
	for _, tc := range []struct {
		targets []string
		source  map[string]any
	}{
		{[]string{"alice"}, nil},
		{[]string{"user:has space@example.com"}, nil},
		{[]string{"all"}, map[string]any{"kind": "issue", "id": "bad id"}},
	} {
		_, err := svc.Post(context.Background(), tc.targets, "test", "", tc.source, "")
		var invalid *event.InvalidInputError
		if !errors.As(err, &invalid) {
			t.Fatalf("invalid Post succeeded: %v", err)
		}
		if refs() != before {
			t.Fatal("invalid Post appended event")
		}
	}
}
