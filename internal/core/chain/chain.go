// Package chain reads and writes githive's append-only commit chains using
// go-git for local object/ref access (docs/02-data-model.md「エンティティ =
// 追記専用コミットチェーン」, ADR-0002). It knows nothing about what a
// particular feature's tree layout means; internal/core/materialize decides
// tree contents, this package just persists them as commits and reads them
// back.
package chain

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// ZeroHash represents "no parent" (a chain root commit).
var ZeroHash = plumbing.ZeroHash

// OpenRepository opens a repository rooted at dir, whether it is a normal
// (non-bare) repo or a bare one.
func OpenRepository(dir string) (*git.Repository, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("chain: open repository at %s: %w", dir, err)
	}
	return repo, nil
}

// Signature is the author/committer identity for a new commit. Per
// docs/02-data-model.md「コミットの規約」, Email must equal the event's actor.
type Signature = object.Signature

// buildMessage renders the two-part commit message: a human summary line
// (truncated to 72 runes) followed by a blank line and the event's canonical
// JSON (docs/02-data-model.md「コミットの規約」「イベント封筒」).
func buildMessage(summary string, env *event.Envelope) (string, error) {
	line := truncateSummary(summary)
	body, err := env.CanonicalJSON()
	if err != nil {
		return "", fmt.Errorf("chain: encode event: %w", err)
	}
	return line + "\n\n" + string(body), nil
}

func truncateSummary(s string) string {
	runes := []rune(s)
	if len(runes) <= 72 {
		return s
	}
	return string(runes[:72])
}

// AppendEvent creates a new commit whose tree is exactly `files` (materialize's
// computed state after folding in env) and whose message encodes env, with
// parent set to parent (use ZeroHash for a chain root). It does not move any
// ref; callers perform the ref update themselves (typically via gitx's CAS
// update-ref, per docs/03-sync-and-concurrency.md).
func AppendEvent(repo *git.Repository, parent plumbing.Hash, env *event.Envelope, summary string, files map[string][]byte, sig Signature) (plumbing.Hash, error) {
	if err := env.Validate(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("chain: invalid envelope: %w", err)
	}
	treeHash, err := WriteTree(repo, files)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	msg, err := buildMessage(summary, env)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	commit := &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   msg,
		TreeHash:  treeHash,
	}
	if parent != plumbing.ZeroHash {
		commit.ParentHashes = []plumbing.Hash{parent}
	}
	return encodeCommit(repo, commit)
}

// AppendMerge creates a two-parent merge commit with message "merge:
// event-union" and no event body (docs/02-data-model.md「コミットの規約」,
// docs/03-sync-and-concurrency.md「event-union マージ」). tree is the fold
// result over the union of both sides' events.
func AppendMerge(repo *git.Repository, parents [2]plumbing.Hash, files map[string][]byte, sig Signature) (plumbing.Hash, error) {
	treeHash, err := WriteTree(repo, files)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	commit := &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      "merge: event-union",
		TreeHash:     treeHash,
		ParentHashes: []plumbing.Hash{parents[0], parents[1]},
	}
	return encodeCommit(repo, commit)
}

func encodeCommit(repo *git.Repository, commit *object.Commit) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("chain: encode commit: %w", err)
	}
	h, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("chain: store commit: %w", err)
	}
	return h, nil
}

// ExtractEnvelope splits a commit's message into its summary line and event
// JSON body, decoding the event. Merge commits (docs/02-data-model.md:
// "マージコミットのメッセージは merge: event-union とし、イベントを含まない")
// have no event body and ExtractEnvelope returns (nil, nil) for them.
func ExtractEnvelope(commit *object.Commit) (*event.Envelope, error) {
	idx := strings.Index(commit.Message, "\n\n")
	if idx < 0 {
		return nil, nil
	}
	body := commit.Message[idx+2:]
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	env, err := event.Decode([]byte(body))
	if err != nil {
		return nil, fmt.Errorf("chain: commit %s: %w", commit.Hash, err)
	}
	return env, nil
}

// walkDFS is the shared traversal behind WalkChain, WalkChainSince, and
// WalkCommits: a DFS over the commit DAG reachable from head (following all
// parents, so it works across event-union merge commits too), visiting each
// reachable hash at most once (diamonds from merges are deduplicated).
// visit is called once per unvisited commit; returning descend=false stops
// the traversal at that commit without pushing its parents onto the stack
// (used by WalkChainSince's since-cutoff pruning — WalkChain and
// WalkCommits always return descend=true).
func walkDFS(repo *git.Repository, head plumbing.Hash, visit func(*object.Commit) (descend bool, err error)) error {
	if head == plumbing.ZeroHash {
		return nil
	}
	visited := map[plumbing.Hash]bool{}
	stack := []plumbing.Hash{head}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[h] {
			continue
		}
		visited[h] = true

		commit, err := object.GetCommit(repo.Storer, h)
		if err != nil {
			return fmt.Errorf("chain: get commit %s: %w", h, err)
		}
		descend, err := visit(commit)
		if err != nil {
			return err
		}
		if descend {
			stack = append(stack, commit.ParentHashes...)
		}
	}
	return nil
}

// WalkChain traverses the commit DAG reachable from head (following all
// parents, so it works across event-union merge commits too) and returns
// every event envelope found, in no particular order. Duplicate commits
// reachable via multiple paths (diamonds from merges) are visited once.
// Callers must sort by envelope ID before folding
// (docs/02-data-model.md「イベントの全順序と実体化」).
func WalkChain(repo *git.Repository, head plumbing.Hash) ([]*event.Envelope, error) {
	var envelopes []*event.Envelope
	err := walkDFS(repo, head, func(commit *object.Commit) (bool, error) {
		env, err := ExtractEnvelope(commit)
		if err != nil {
			return false, err
		}
		if env != nil {
			envelopes = append(envelopes, env)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return envelopes, nil
}

// WalkChainSince is like WalkChain but stops descending into a commit's
// parent once it observes an event envelope with TS < since (the entity
// chain is append-only, so under normal clock behavior a single-parent
// commit's ancestors are never newer than it — docs/02-data-model.md
// 「エンティティ = 追記専用コミットチェーン」). Merge commits (ExtractEnvelope
// returns nil envelope, chain.go:125-127) carry no ts of their own and
// combine two potentially unrelated timelines, so pruning never happens at
// a merge commit: both parents are always traversed regardless of since.
//
// This is a best-effort optimization, not a correctness guarantee: per
// docs/03-sync-and-concurrency.md「時計異常への防御」, a client with a
// backward-skewed local clock is only warned, never rejected, so a
// single-parent commit's ts can in rare cases be lower than its parent's.
// Under that scenario this function can silently omit an ancestor event
// that actually qualifies (ts >= since). See docs/adr/0012-walkchainsince-
// clock-skew-best-effort.md for the accepted-risk rationale (this affects
// only `githive log --since` result completeness, not determinism/fold
// semantics — logapp does not go through internal/core/materialize).
//
// since must be in the same RFC3339 UTC millisecond format as envelope TS
// fields for lexical comparison to be a valid time comparison (caller's
// responsibility — mirrors internal/app/logapp.Service.List's existing
// validation via event.IsValidTimestamp before calling this).
func WalkChainSince(repo *git.Repository, head plumbing.Hash, since string) ([]*event.Envelope, error) {
	var envelopes []*event.Envelope
	err := walkDFS(repo, head, func(commit *object.Commit) (bool, error) {
		env, err := ExtractEnvelope(commit)
		if err != nil {
			return false, err
		}
		if env != nil && env.TS < since {
			// Single-event commit older than the cutoff: its ancestors are
			// only ever older still, so stop descending here.
			return false, nil
		}
		if env != nil {
			envelopes = append(envelopes, env)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return envelopes, nil
}

// WalkCommits traverses the commit DAG reachable from head (like
// WalkChain) but returns the raw commit objects instead of just their
// envelopes, for callers that need commit-level metadata (hash, author/
// committer identity and time, signature) - e.g. core/sign's per-commit
// signature verification (docs/11-security.md「SSH 署名」).
func WalkCommits(repo *git.Repository, head plumbing.Hash) ([]*object.Commit, error) {
	var commits []*object.Commit
	err := walkDFS(repo, head, func(commit *object.Commit) (bool, error) {
		commits = append(commits, commit)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return commits, nil
}

// ReadTree reads a commit's full tree into a flat map of path -> file
// content, recursing into subdirectories. Paths always use "/" regardless of
// platform (docs/01-architecture.md「プラットフォーム」).
func ReadTree(repo *git.Repository, commitHash plumbing.Hash) (map[string][]byte, error) {
	commit, err := object.GetCommit(repo.Storer, commitHash)
	if err != nil {
		return nil, fmt.Errorf("chain: get commit %s: %w", commitHash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("chain: get tree for %s: %w", commitHash, err)
	}
	files := map[string][]byte{}
	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("chain: walk tree: %w", err)
		}
		if entry.Mode == filemode.Dir {
			continue
		}
		blob, err := object.GetBlob(repo.Storer, entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("chain: get blob %s (%s): %w", entry.Hash, name, err)
		}
		reader, err := blob.Reader()
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(reader); err != nil {
			reader.Close()
			return nil, err
		}
		reader.Close()
		files[name] = buf.Bytes()
	}
	return files, nil
}

// fileTree is a node in the in-memory tree built from a flat path->content
// map before it is written out as nested git tree objects.
type fileTree struct {
	blob     []byte
	isBlob   bool
	children map[string]*fileTree
}

func newDirNode() *fileTree {
	return &fileTree{children: map[string]*fileTree{}}
}

// WriteTree writes files (a flat map of "/"-separated relative path ->
// content) as nested git tree objects and returns the root tree hash.
func WriteTree(repo *git.Repository, files map[string][]byte) (plumbing.Hash, error) {
	root := newDirNode()
	for path, content := range files {
		parts := strings.Split(path, "/")
		cur := root
		for i, part := range parts {
			if i == len(parts)-1 {
				cur.children[part] = &fileTree{blob: content, isBlob: true}
				continue
			}
			next, ok := cur.children[part]
			if !ok || next.isBlob {
				next = newDirNode()
				cur.children[part] = next
			}
			cur = next
		}
	}
	return writeTreeNode(repo, root)
}

func writeTreeNode(repo *git.Repository, node *fileTree) (plumbing.Hash, error) {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)

	tree := &object.Tree{}
	for _, name := range names {
		child := node.children[name]
		if child.isBlob {
			h, err := writeBlob(repo, child.blob)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			tree.Entries = append(tree.Entries, object.TreeEntry{
				Name: name,
				Mode: filemode.Regular,
				Hash: h,
			})
			continue
		}
		h, err := writeTreeNode(repo, child)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		tree.Entries = append(tree.Entries, object.TreeEntry{
			Name: name,
			Mode: filemode.Dir,
			Hash: h,
		})
	}

	obj := repo.Storer.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("chain: encode tree: %w", err)
	}
	return repo.Storer.SetEncodedObject(obj)
}

// AdvanceRef performs a local compare-and-swap ref update
// (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」): it sets
// refName to newHash only if it currently points at oldHash (use
// gitx.ZeroOID for "must not exist yet"). This shells out to system git via
// gitx rather than go-git, because go-git's CheckAndSetReference cannot
// correctly express "ref must not already exist" (there is no packed/loose
// ref to compare against yet), whereas `git update-ref <ref> <new> <old>`
// handles that case natively.
func AdvanceRef(dir string, refName plumbing.ReferenceName, newHash plumbing.Hash, oldOID string) error {
	r := gitx.New(dir)
	if err := r.UpdateRef(context.Background(), refName.String(), newHash.String(), oldOID); err != nil {
		return fmt.Errorf("chain: %w", err)
	}
	return nil
}

func writeBlob(repo *git.Repository, content []byte) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(content); err != nil {
		w.Close()
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}
