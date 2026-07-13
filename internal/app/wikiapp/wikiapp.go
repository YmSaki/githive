// Package wikiapp implements the read side of `githive wiki` (show/log):
// browsing the wiki's plain git branch refs/projects/wiki/main without a
// worktree (docs/features/wiki.md, docs/10-cli-spec.md「コマンド体系」).
//
// Unlike every other app package, wiki does NOT use event sourcing
// (docs/features/wiki.md「イベントソーシングを使わない」): there is no event
// envelope, reducer, fold, or spec vector — the wiki is an ordinary git tree
// read through internal/core/gitx, so this package never touches
// internal/core/materialize and carries no fold-semantics obligations.
package wikiapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// ErrNotFound is returned by Show when the path — or the wiki ref itself —
// does not exist. cmd/githive maps it to the not_found exit code.
var ErrNotFound = errors.New("wikiapp: path not found in wiki")

// Service provides read and write access to the wiki for the repository at
// Dir. Read (Show/Log) lives in wikiapp.go; write (Edit/Save) in write.go.
type Service struct {
	Dir string

	// afterMerge is a test-only seam invoked once after a clean local wiki
	// merge and before the push, letting tests advance the remote to exercise
	// the push-race retry. It is nil in production.
	afterMerge func()
}

// New returns a Service rooted at dir.
func New(dir string) *Service {
	return &Service{Dir: dir}
}

// Show returns the raw bytes of path in the wiki tree, equivalent to
// `git show refs/projects/wiki/main:<path>`. A missing ref or path yields
// ErrNotFound (the underlying git error is preserved in the message).
func (s *Service) Show(ctx context.Context, path string) ([]byte, error) {
	b, err := gitx.New(s.Dir).Show(ctx, refspace.WikiMainRef, path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotFound, path, err)
	}
	return b, nil
}

// Entry is one wiki commit. Like logapp.Entry it is a map (not a struct) so
// internal/core/event's canonical JSON encoder — which cliout reuses — can
// render it. Keys: hash, author, date, subject.
type Entry = map[string]any

// Log returns the wiki's commit history, most-recent first, optionally
// restricted to path (empty means the whole tree). A missing wiki ref yields
// an empty slice with no error: a repository may legitimately have no wiki
// yet (docs/features/wiki.md).
func (s *Service) Log(ctx context.Context, path string) ([]Entry, error) {
	commits, err := gitx.New(s.Dir).Log(ctx, refspace.WikiMainRef, path)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(commits))
	for _, c := range commits {
		out = append(out, Entry{
			"hash":    c.Hash,
			"author":  c.Author,
			"date":    c.Date,
			"subject": c.Subject,
		})
	}
	return out, nil
}
