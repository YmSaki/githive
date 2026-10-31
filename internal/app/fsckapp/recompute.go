package fsckapp

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// checkMaterialization independently recomputes what an entity chain's
// trees and checkpoints must contain (docs/11-security.md「中身の正しさは
// 決定的実体化の再計算で独立に検証できる」, ADR-0003, docs/14 シナリオ 6):
//
//   - tree_mismatch: head's tree must equal TreeFiles(fold(every event
//     reachable from head)) byte for byte;
//   - checkpoint_state_mismatch: every checkpoint's data.state must equal
//     fold(every event reachable from the checkpoint's commit), since that
//     snapshot is what readers restart their fold from (ADR-0016).
//
// Only head is recomputed, not every intermediate commit: readers only ever
// materialize from head, and one fold per ref keeps fsck linear in the
// chain length (docs/14 性能目標). A chain whose envelopes cannot be read is
// skipped here; validateChain has already reported it.
func checkMaterialization(repo *git.Repository, ref string, feature refspace.Feature, head plumbing.Hash) []Finding {
	fw, ok := featureWriters[feature]
	if !ok {
		return nil
	}
	events, err := chain.WalkChain(repo, head)
	if err != nil {
		return nil
	}

	var findings []Finding
	want, err := fw.treeFiles(fw.registry.Fold(events))
	if err != nil {
		return []Finding{{Ref: ref, Feature: string(feature), Commit: head.String(), Code: "tree_render_failed", Message: err.Error()}}
	}
	got, err := chain.ReadTree(repo, head)
	if err != nil {
		return []Finding{{Ref: ref, Feature: string(feature), Commit: head.String(), Code: "read_tree_failed", Message: err.Error()}}
	}
	if diff := diffTrees(want, got); len(diff) > 0 {
		findings = append(findings, Finding{
			Ref: ref, Feature: string(feature), Commit: head.String(), Path: diff[0], Code: "tree_mismatch",
			Message: fmt.Sprintf("head tree differs from the recomputed materialization at %d path(s): %s", len(diff), strings.Join(diff, ", ")),
		})
	}

	commits, err := chain.WalkCommits(repo, head)
	if err != nil {
		return findings
	}
	for _, c := range commits {
		env, err := chain.ExtractEnvelope(c)
		if err != nil || env == nil || !isCheckpoint(env.Kind) {
			continue
		}
		f, ok := checkCheckpointState(repo, ref, feature, fw.registry, c.Hash, env)
		if !ok {
			findings = append(findings, f)
		}
	}
	return findings
}

func checkCheckpointState(repo *git.Repository, ref string, feature refspace.Feature, registry *materialize.Registry, commit plumbing.Hash, env *event.Envelope) (Finding, bool) {
	mismatch := func(msg string) (Finding, bool) {
		return Finding{Ref: ref, Feature: string(feature), Commit: commit.String(), Code: "checkpoint_state_mismatch", Message: msg}, false
	}
	events, err := chain.WalkChain(repo, commit)
	if err != nil {
		return Finding{}, true // validateChain reports unreadable envelopes
	}
	wantMap, err := materialize.StateToMap(registry.Fold(events))
	if err != nil {
		return mismatch(fmt.Sprintf("cannot render recomputed state: %v", err))
	}
	want, err := event.Encode(wantMap)
	if err != nil {
		return mismatch(fmt.Sprintf("cannot encode recomputed state: %v", err))
	}
	stored, ok := env.Data["state"]
	if !ok {
		return mismatch("checkpoint has no data.state")
	}
	got, err := event.Encode(stored)
	if err != nil {
		return mismatch(fmt.Sprintf("cannot encode checkpoint data.state: %v", err))
	}
	if !bytes.Equal(want, got) {
		return mismatch(fmt.Sprintf("checkpoint %s data.state does not match the fold of the %d event(s) reachable from its commit", env.ID, countNonCheckpoint(events)))
	}
	return Finding{}, true
}

// diffTrees returns the sorted paths whose content differs between want and
// got, including paths present on only one side.
func diffTrees(want, got map[string][]byte) []string {
	var paths []string
	for p, w := range want {
		if g, ok := got[p]; !ok || !bytes.Equal(w, g) {
			paths = append(paths, p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}
