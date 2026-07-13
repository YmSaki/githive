package wikiapp

// Write side of `githive wiki` (edit/save). Unlike every other app package
// this does NOT use event sourcing (docs/features/wiki.md
// 「イベントソーシングを使わない」): editing is a temporary git worktree, and
// reconciliation is git's ordinary 3-way merge — never event-union, fold, or
// internal/core/materialize. This file therefore imports only gitx (git
// plumbing), refspace (the wiki ref name), and wikifs (pure filename/asset
// portability rules); it deliberately touches none of the event-sourcing core.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wikifs"
)

// worktreeConfigKey records the active wiki edit worktree so a later `wiki
// save` (a separate process) can find it. Edit sets it; a successful Save
// clears it.
const worktreeConfigKey = "githive.wiki.worktree"

// wikiPushRetries bounds the merge→push loop when the remote wiki keeps
// advancing between our fetch and push (the push-race).
const wikiPushRetries = 5

// ValidationError reports that the wiki tree violates the OS-portability /
// asset-size rules (wikifs). It is returned before anything is committed, so a
// rejected tree never enters history. Maps to exit 4 (verify-failed); the
// Violations are surfaced in --json error.data.
type ValidationError struct {
	Violations []wikifs.Violation
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("wiki: %d filename/asset validation violation(s); fix the offending path(s) in the worktree and re-run `githive wiki save`", len(e.Violations))
}

// ConflictError reports that the merge of the remote wiki into the local edit
// left conflict markers a human must resolve. The local commit is already
// saved and the worktree keeps the markers + MERGE_HEAD, so the human resolves
// them and re-runs save. Maps to exit 3 (retryable).
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("wiki: merge conflict in %d path(s): %s; resolve the conflict markers in the worktree and re-run `githive wiki save`", len(e.Paths), strings.Join(e.Paths, ", "))
}

var (
	// ErrPushRaceExhausted means the remote wiki advanced faster than we could
	// push after wikiPushRetries clean re-merges. The merged commit is saved
	// locally; re-running save retries. Maps to exit 3 (retryable).
	ErrPushRaceExhausted = errors.New("wikiapp: wiki push retries exhausted (remote kept advancing); local commit saved, re-run `githive wiki save`")

	// ErrNoEditInProgress means Save was invoked without a prior `wiki edit`
	// (no worktree recorded). Maps to exit 1.
	ErrNoEditInProgress = errors.New("wikiapp: no wiki edit in progress (run `githive wiki edit` first)")

	// ErrNothingToSave means the worktree has no changes to commit. Maps to
	// exit 1.
	ErrNothingToSave = errors.New("wikiapp: no changes to save")
)

// SaveResult is the outcome of a successful Save.
type SaveResult struct {
	CommitHash string
	Pushed     bool
}

// Edit creates a temporary git worktree checked out at the wiki ref (or at an
// empty tree if the wiki does not exist yet) and records its path so a later
// Save can find it, returning the worktree path for the human to edit. Because
// the wiki is a plain git tree, this never constructs an event, fold, or
// entitychain and never enters internal/core/materialize.
//
// The keep flag documents intent; it does not change worktree lifetime. `wiki
// edit` and `wiki save` are separate processes, so the worktree always persists
// between them and Save removes it on success. With --keep the human keeps the
// worktree to drive ordinary `git -C <dir> ...` commands before saving; without
// it the worktree is still the handoff between edit and save. keep is surfaced
// so a future inline-editor integration can auto-remove a non-kept worktree.
func (s *Service) Edit(ctx context.Context, keep bool) (string, error) {
	r := gitx.New(s.Dir)
	dir, err := r.WorktreeAdd(ctx, refspace.WikiMainRef)
	if err != nil {
		return "", err
	}
	if err := r.ConfigSet(ctx, worktreeConfigKey, dir); err != nil {
		_ = r.WorktreeRemove(ctx, dir)
		return "", err
	}
	return dir, nil
}

// Save validates the recorded edit worktree, commits it, reconciles with the
// remote wiki via git's 3-way merge, and (unless noSync) pushes. On a merge
// conflict it returns *ConflictError with the conflicted paths and leaves the
// worktree with markers for the human to resolve and re-run. On a validation
// failure it returns *ValidationError before committing. remote is the sync
// remote (typically "origin"); noSync commits and advances the local ref only.
//
// The worktree is removed only on successful completion; a validation reject,
// a conflict, or an error retains it so the human can fix the tree / resolve
// the markers and re-run save, which then completes and cleans up.
func (s *Service) Save(ctx context.Context, msg, remote string, noSync bool) (SaveResult, error) {
	r := gitx.New(s.Dir)
	worktreeDir, err := r.ConfigGet(ctx, worktreeConfigKey)
	if err != nil {
		return SaveResult{}, err
	}
	if worktreeDir == "" {
		return SaveResult{}, ErrNoEditInProgress
	}

	inMerge, err := r.MergeInProgress(ctx, worktreeDir)
	if err != nil {
		return SaveResult{}, err
	}
	if inMerge {
		return s.finishConflictRerun(ctx, r, worktreeDir, msg, remote, noSync)
	}
	return s.saveNormal(ctx, r, worktreeDir, msg, remote, noSync)
}

// saveNormal handles a first save of an edit: validate, commit, then merge/push.
func (s *Service) saveNormal(ctx context.Context, r *gitx.Runner, worktreeDir, msg, remote string, noSync bool) (SaveResult, error) {
	if err := validateTree(worktreeDir); err != nil {
		return SaveResult{}, err
	}

	changed, err := r.HasChanges(ctx, worktreeDir)
	if err != nil {
		return SaveResult{}, err
	}
	if !changed {
		return SaveResult{}, ErrNothingToSave
	}

	if _, err := r.CommitAll(ctx, worktreeDir, msg); err != nil {
		return SaveResult{}, err
	}

	if noSync {
		return s.advanceLocalAndFinish(ctx, r, worktreeDir)
	}
	return s.mergeAndPush(ctx, r, worktreeDir, remote)
}

// finishConflictRerun completes a save that previously stopped on conflicts:
// the human resolved the markers, so verify none remain, validate, conclude the
// merge commit, and push.
func (s *Service) finishConflictRerun(ctx context.Context, r *gitx.Runner, worktreeDir, msg, remote string, noSync bool) (SaveResult, error) {
	// The paths still unmerged in the index are the ones the human had to
	// resolve; capture them before staging clears their unmerged status.
	unmerged, err := r.ConflictedPaths(ctx, worktreeDir)
	if err != nil {
		return SaveResult{}, err
	}
	// Stage the human's edits so the resolved files leave the unmerged state.
	if err := r.StageAll(ctx, worktreeDir); err != nil {
		return SaveResult{}, err
	}
	// Refuse to record a merge that still carries conflict markers: staging
	// alone would silently commit them, so verify the human actually resolved
	// the previously-conflicted files.
	if bad := filesWithConflictMarkers(worktreeDir, unmerged); len(bad) > 0 {
		return SaveResult{}, &ConflictError{Paths: bad}
	}
	if err := validateTree(worktreeDir); err != nil {
		return SaveResult{}, err
	}
	if _, err := r.CommitAll(ctx, worktreeDir, msg); err != nil {
		return SaveResult{}, err
	}
	if noSync {
		return s.advanceLocalAndFinish(ctx, r, worktreeDir)
	}
	return s.pushMerged(ctx, r, worktreeDir, remote)
}

// mergeAndPush fetches and 3-way-merges the remote wiki into the worktree HEAD,
// advances the local ref, and pushes — retrying the whole cycle when a push
// race (the remote advanced between our fetch and push) rejects the push.
func (s *Service) mergeAndPush(ctx context.Context, r *gitx.Runner, worktreeDir, remote string) (SaveResult, error) {
	trackingRef, err := refspace.RemoteTrackingRef(refspace.WikiMainRef)
	if err != nil {
		return SaveResult{}, err
	}
	for attempt := 0; attempt < wikiPushRetries; attempt++ {
		conflicts, err := r.MergeWikiRemote(ctx, worktreeDir, remote, refspace.WikiMainRef, trackingRef)
		if err != nil {
			return SaveResult{}, err
		}
		if len(conflicts) > 0 {
			// Leave the worktree (markers + MERGE_HEAD) for the human.
			return SaveResult{}, &ConflictError{Paths: conflicts}
		}
		res, retry, err := s.pushMergedOnce(ctx, r, worktreeDir, remote)
		if err != nil {
			return SaveResult{}, err
		}
		if !retry {
			return res, nil
		}
		// Push lost the race; loop to incorporate the newer remote.
	}
	return SaveResult{}, ErrPushRaceExhausted
}

// pushMerged advances the local ref to the (already clean) worktree HEAD and
// pushes; a push race routes back into the full merge/push retry loop.
func (s *Service) pushMerged(ctx context.Context, r *gitx.Runner, worktreeDir, remote string) (SaveResult, error) {
	res, retry, err := s.pushMergedOnce(ctx, r, worktreeDir, remote)
	if err != nil {
		return SaveResult{}, err
	}
	if !retry {
		return res, nil
	}
	return s.mergeAndPush(ctx, r, worktreeDir, remote)
}

// pushMergedOnce advances refs/projects/wiki/main to the current worktree HEAD
// (CAS) and pushes it once. retry is true when the push was rejected by a race
// (the caller should re-fetch/re-merge and try again); on success it removes
// the worktree.
func (s *Service) pushMergedOnce(ctx context.Context, r *gitx.Runner, worktreeDir, remote string) (res SaveResult, retry bool, err error) {
	merged, err := r.WorktreeHead(ctx, worktreeDir)
	if err != nil {
		return SaveResult{}, false, err
	}
	curTip, err := r.RevParse(ctx, refspace.WikiMainRef)
	if err != nil {
		return SaveResult{}, false, err
	}
	if err := r.UpdateRef(ctx, refspace.WikiMainRef, merged, orZero(curTip)); err != nil {
		return SaveResult{}, false, err
	}

	// Test seam: exercise the push-race by advancing the remote after a clean
	// local merge but before our push. nil in production.
	if s.afterMerge != nil {
		hook := s.afterMerge
		s.afterMerge = nil
		hook()
	}

	results, err := r.Push(ctx, remote, refspace.WikiMainRef+":"+refspace.WikiMainRef)
	if err != nil {
		return SaveResult{}, false, err
	}
	if !pushOK(results) {
		return SaveResult{}, true, nil // rejected: race, caller retries
	}
	if err := s.cleanup(ctx, r, worktreeDir); err != nil {
		return SaveResult{}, false, err
	}
	return SaveResult{CommitHash: merged, Pushed: true}, false, nil
}

// advanceLocalAndFinish is the --no-sync tail: advance the local wiki ref to
// the worktree HEAD and clean up, with no fetch/merge/push.
func (s *Service) advanceLocalAndFinish(ctx context.Context, r *gitx.Runner, worktreeDir string) (SaveResult, error) {
	merged, err := r.WorktreeHead(ctx, worktreeDir)
	if err != nil {
		return SaveResult{}, err
	}
	curTip, err := r.RevParse(ctx, refspace.WikiMainRef)
	if err != nil {
		return SaveResult{}, err
	}
	if err := r.UpdateRef(ctx, refspace.WikiMainRef, merged, orZero(curTip)); err != nil {
		return SaveResult{}, err
	}
	if err := s.cleanup(ctx, r, worktreeDir); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{CommitHash: merged, Pushed: false}, nil
}

// cleanup removes the worktree and clears the recorded worktree config after a
// successful save.
func (s *Service) cleanup(ctx context.Context, r *gitx.Runner, worktreeDir string) error {
	rmErr := r.WorktreeRemove(ctx, worktreeDir)
	cfgErr := r.ConfigUnset(ctx, worktreeConfigKey)
	if rmErr != nil {
		return rmErr
	}
	return cfgErr
}

// validateTree walks worktreeDir and runs the wikifs portability/asset rules,
// returning *ValidationError if any file violates them.
func validateTree(worktreeDir string) error {
	files, err := walkWikiFiles(worktreeDir)
	if err != nil {
		return err
	}
	if vs := wikifs.Check(files); len(vs) > 0 {
		return &ValidationError{Violations: vs}
	}
	return nil
}

// walkWikiFiles lists every file in worktreeDir as a slash-separated
// repo-relative path with its size, skipping git's own worktree metadata (the
// ".git" file at the worktree root).
func walkWikiFiles(worktreeDir string) ([]wikifs.File, error) {
	var files []wikifs.File
	err := filepath.WalkDir(worktreeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(worktreeDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, wikifs.File{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// filesWithConflictMarkers returns those of paths whose worktree content still
// contains git conflict markers, i.e. the human re-ran save without finishing
// the resolution. A path resolved by deletion (file gone) is treated as
// resolved.
func filesWithConflictMarkers(worktreeDir string, paths []string) []string {
	var bad []string
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(worktreeDir, filepath.FromSlash(p)))
		if err != nil {
			continue // resolved by deletion or otherwise unreadable
		}
		if hasConflictMarker(b) {
			bad = append(bad, p)
		}
	}
	return bad
}

// hasConflictMarker reports whether content still carries an unresolved git
// 3-way merge marker. It keys off the labelled start/end markers ("<<<<<<< "
// and ">>>>>>> ", which always carry a branch/ref label) rather than the bare
// "=======" separator: a real unresolved conflict always contains the
// labelled markers, while a lone line of exactly "=======" is legitimate wiki
// content (e.g. a Markdown setext-H1 underline), so matching it would falsely
// reject a resolved page on the conflict re-run path.
func hasConflictMarker(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
			return true
		}
	}
	return false
}

func pushOK(results []gitx.PushResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, res := range results {
		if !res.OK {
			return false
		}
	}
	return true
}

// orZero maps an empty (absent) OID to git's zero OID, the compare-and-swap
// "must not exist yet" sentinel for UpdateRef.
func orZero(oid string) string {
	if oid == "" {
		return gitx.ZeroOID
	}
	return oid
}
