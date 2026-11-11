package usersapp

import (
	"errors"
	"fmt"

	"github.com/ymsaki/githive/internal/core/materialize"
)

// ErrEmailExists means another registry user already owns an email.
var ErrEmailExists = errors.New("usersapp: email already belongs to another user")

func emailAvailable(state *materialize.State, username, email string) error {
	if email == "" {
		return nil
	}
	for _, owner := range materialize.EmailOwners(state)[email] {
		if owner != username {
			return fmt.Errorf("%w: %q belongs to %q", ErrEmailExists, email, owner)
		}
	}
	return nil
}
