package fsckapp

import (
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wikifs"
)

// checkWikiFilenames reads the wiki tree (a plain git branch, not an event
// chain) and runs the shared OS-portability rules over its files
// (internal/core/wikifs), turning each Violation into a Finding. This is the
// only check the wiki ref gets; it is deliberately never event-schema
// validated (docs/features/wiki.md: wiki is the one feature not event-sourced).
func checkWikiFilenames(repo *git.Repository, ref string, head plumbing.Hash) ([]Finding, error) {
	files, err := chain.ReadTree(repo, head)
	if err != nil {
		return nil, err
	}
	wf := make([]wikifs.File, 0, len(files))
	for path, content := range files {
		wf = append(wf, wikifs.File{Path: path, Size: int64(len(content))})
	}
	var findings []Finding
	for _, v := range wikifs.Check(wf) {
		findings = append(findings, Finding{
			Ref:     ref,
			Feature: string(refspace.FeatureWiki),
			Path:    v.Path,
			Code:    v.Code,
			Message: v.Message,
		})
	}
	return findings, nil
}
