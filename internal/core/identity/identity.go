// Package identity resolves the local git identity used both as an event's
// actor (docs/02-data-model.md「actor」, ADR-0009: always git's user.email)
// and as a commit's author/committer signature.
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// ErrNotConfigured means git's user.email is unset
// (docs/10-cli-spec.md「終了コード」5: 環境不備).
var ErrNotConfigured = errors.New("identity: git user.email is not configured")

// Resolve reads user.email (required) and user.name (falls back to email)
// from git config at dir, returning a ready-to-use commit signature.
func Resolve(ctx context.Context, dir string) (chain.Signature, error) {
	r := gitx.New(dir)
	email, err := r.ConfigGet(ctx, "user.email")
	if err != nil {
		return chain.Signature{}, err
	}
	if email == "" {
		return chain.Signature{}, ErrNotConfigured
	}
	name, err := r.ConfigGet(ctx, "user.name")
	if err != nil {
		return chain.Signature{}, err
	}
	if name == "" {
		name = email
	}
	return chain.Signature{Name: name, Email: email, When: time.Now()}, nil
}
