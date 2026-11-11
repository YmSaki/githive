package usersapp

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

func TestAddUserRejectsUnknownKindWithoutAppending(t *testing.T) {
	svc := newTestRepo(t)
	err := svc.AddUser(context.Background(), "alice", "", "alice@example.com", "robot", nil)
	var invalid *event.InvalidInputError
	if !errors.As(err, &invalid) {
		t.Fatalf("unknown user kind accepted: %v", err)
	}
	refs, err := exec.Command("git", "-C", svc.Dir, "for-each-ref", "--format=%(refname)", RegistryRef).CombinedOutput()
	if err != nil || len(refs) != 0 {
		t.Fatalf("invalid user kind created registry: %s (%v)", refs, err)
	}
}
