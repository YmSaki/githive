// Package gitx wraps invocations of the system git binary for the
// operations that must go over the network or use plumbing not worth
// reimplementing: version checks, fetch, push, for-each-ref, and
// compare-and-swap ref updates. Local object/tree/commit creation lives in
// internal/core/chain via go-git instead (docs/01-architecture.md「依存規則」,
// ADR-0002).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MinVersion is the lowest system git version githive supports (SSH signing
// support; docs/01-architecture.md「プラットフォーム」).
var MinVersion = Version{Major: 2, Minor: 34, Patch: 0}

// ZeroOID is git's all-zero object id, used as the "ref does not exist yet"
// old-value sentinel for compare-and-swap ref updates.
const ZeroOID = "0000000000000000000000000000000000000000"

// Sentinel errors core/app layers can type-switch on
// (docs/01-architecture.md「エラー処理方針」).
var (
	ErrNonFastForward = errors.New("gitx: non-fast-forward")
	ErrRefCASMismatch = errors.New("gitx: ref update rejected (old value mismatch)")
)

// Version is a parsed `git version` result.
type Version struct {
	Major, Minor, Patch int
}

// Less reports whether v is strictly older than other.
func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Runner executes system git against a fixed repository directory
// (typically the bare or working repo root; githive itself never uses a
// worktree except for wiki edits).
type Runner struct {
	Dir string
}

// New returns a Runner rooted at dir.
func New(dir string) *Runner {
	return &Runner{Dir: dir}
}

func (r *Runner) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("gitx: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// CheckVersion runs `git version` and errors if it is older than MinVersion
// or unparsable.
func CheckVersion(ctx context.Context) (Version, error) {
	cmd := exec.CommandContext(ctx, "git", "version")
	out, err := cmd.Output()
	if err != nil {
		return Version{}, fmt.Errorf("gitx: git version: %w", err)
	}
	v, err := parseVersion(string(out))
	if err != nil {
		return Version{}, err
	}
	if v.Less(MinVersion) {
		return v, fmt.Errorf("gitx: system git %s is older than required %s", v, MinVersion)
	}
	return v, nil
}

func parseVersion(s string) (Version, error) {
	// "git version 2.43.0" (possibly with a vendor suffix like ".windows.1").
	fields := strings.Fields(strings.TrimSpace(s))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			parts := strings.SplitN(fields[i+1], ".", 4)
			if len(parts) < 3 {
				break
			}
			major, err1 := strconv.Atoi(parts[0])
			minor, err2 := strconv.Atoi(parts[1])
			patch, err3 := strconv.Atoi(strings.TrimSuffix(parts[2], "\n"))
			if err1 != nil || err2 != nil || err3 != nil {
				break
			}
			return Version{major, minor, patch}, nil
		}
	}
	return Version{}, fmt.Errorf("gitx: could not parse git version output: %q", s)
}

// RefEntry is one row of `git for-each-ref` output.
type RefEntry struct {
	OID string
	Ref string
}

// ForEachRef lists refs matching the given patterns, sorted by ref name.
// Patterns are git for-each-ref patterns: a trailing "/" (e.g.
// "refs/projects/") matches everything under that prefix recursively; a
// trailing "/*" only matches one further path segment (git's glob does not
// cross "/" on a bare "*").
func (r *Runner) ForEachRef(ctx context.Context, patterns ...string) ([]RefEntry, error) {
	args := append([]string{"for-each-ref", "--format=%(objectname) %(refname)", "--sort=refname"}, patterns...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var entries []RefEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		entries = append(entries, RefEntry{OID: parts[0], Ref: parts[1]})
	}
	return entries, nil
}

// RevParse resolves ref to its object id. Returns ("", nil) if ref does not
// exist.
func (r *Runner) RevParse(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Show returns the bytes of path at ref, equivalent to
// `git show <ref>:<path>` (docs/features/wiki.md「素の git での読み書き」).
// It errors if ref or path does not exist; read-only callers (wikiapp) map
// that to a not-found result.
func (r *Runner) Show(ctx context.Context, ref, path string) ([]byte, error) {
	return r.run(ctx, "show", ref+":"+path)
}

// LogEntry is one row of `git log` output.
type LogEntry struct {
	Hash    string // full commit hash
	Author  string // author email
	Date    string // author date, strict RFC3339 (git %aI)
	Subject string // commit subject (first line)
}

// Log returns the commit history of ref, most-recent first, equivalent to
// `git log <ref> [-- <path>]`. When ref does not exist it returns an empty
// slice and no error: a repository may legitimately have no wiki yet
// (docs/features/wiki.md; logapp likewise treats a missing wiki ref as
// normal). path, when non-empty, restricts history to commits touching it.
func (r *Runner) Log(ctx context.Context, ref, path string) ([]LogEntry, error) {
	oid, err := r.RevParse(ctx, ref)
	if err != nil {
		return nil, err
	}
	if oid == "" {
		return nil, nil
	}
	// %x1f (unit separator) between fields; subjects (%s) are single-line so
	// newline is a safe record separator.
	args := []string{"log", "--pretty=format:%H%x1f%aE%x1f%aI%x1f%s", ref}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		entries = append(entries, LogEntry{Hash: parts[0], Author: parts[1], Date: parts[2], Subject: parts[3]})
	}
	return entries, nil
}

// IsAncestor reports whether ancestor is an ancestor of (or equal to)
// descendant, per `git merge-base --is-ancestor`. Used by syncapp to decide
// between fast-forward and event-union merge
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
func (r *Runner) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "merge-base", "--is-ancestor", ancestor, descendant)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: merge-base --is-ancestor: %w", err)
}

// ConfigGet reads a git config value (`git config --get key`). It returns
// ("", nil) if the key is unset, distinguishing "unset" from a real error.
func (r *Runner) ConfigGet(ctx context.Context, key string) (string, error) {
	out, err := r.run(ctx, "config", "--get", key)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ConfigGetAll reads all values of a (possibly multi-valued) git config key
// (`git config --get-all key`). Returns an empty slice if unset.
func (r *Runner) ConfigGetAll(ctx context.Context, key string) ([]string, error) {
	out, err := r.run(ctx, "config", "--get-all", key)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// ConfigAdd appends a value to a (possibly multi-valued) git config key
// (`git config --add key value`), without removing existing values.
func (r *Runner) ConfigAdd(ctx context.Context, key, value string) error {
	_, err := r.run(ctx, "config", "--add", key, value)
	return err
}

// ConfigSet replaces a git config key with a single value
// (`git config key value`).
func (r *Runner) ConfigSet(ctx context.Context, key, value string) error {
	_, err := r.run(ctx, "config", key, value)
	return err
}

// VerifyCommitResult is the outcome of one `git verify-commit` check.
type VerifyCommitResult struct {
	// Valid is true if git considers the commit's signature cryptographically
	// good against allowedSignersFile.
	Valid bool
	// Output is git's combined stdout+stderr (verify-commit writes its
	// human-readable "Good/Bad signature" report there), for diagnostics
	// and for extracting the matched principal (docs/11-security.md
	// 「実装上の注意」: 検証はsystem gitに委譲).
	Output string
}

// VerifyCommit runs `git verify-commit` against commitHash using an
// ephemeral gpg.ssh.allowedSignersFile (passed via -c, never written to the
// repo's permanent config), per docs/11-security.md「実装上の注意」
// (allowed_signers は一時ファイルとして都度生成し、検証後に削除する - the
// caller owns creating/removing allowedSignersFile; this just points git at
// it for one invocation).
func (r *Runner) VerifyCommit(ctx context.Context, commitHash, allowedSignersFile string) (VerifyCommitResult, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir,
		"-c", "gpg.ssh.allowedSignersFile="+allowedSignersFile,
		"-c", "gpg.format=ssh",
		"verify-commit", "--raw", commitHash,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return VerifyCommitResult{Valid: true, Output: out.String()}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return VerifyCommitResult{Valid: false, Output: out.String()}, nil
	}
	return VerifyCommitResult{}, fmt.Errorf("gitx: git verify-commit: %w", err)
}

// UpdateRef performs a compare-and-swap ref update: it succeeds only if ref
// currently points at oldOID (use ZeroOID for "must not exist yet").
// (docs/03-sync-and-concurrency.md「クラッシュ安全性とローカル競合」).
func (r *Runner) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	_, err := r.run(ctx, "update-ref", ref, newOID, oldOID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefCASMismatch, err)
	}
	return nil
}

// DeleteRef removes ref, verifying it currently points at oldOID.
func (r *Runner) DeleteRef(ctx context.Context, ref, oldOID string) error {
	_, err := r.run(ctx, "update-ref", "-d", ref, oldOID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefCASMismatch, err)
	}
	return nil
}

// Fetch runs `git fetch remote refspecs...`.
func (r *Runner) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	args := append([]string{"fetch", remote}, refspecs...)
	_, err := r.run(ctx, args...)
	return err
}

// PushResult reports the outcome for one refspec of a push.
type PushResult struct {
	Refspec string
	OK      bool
	Reason  string
}

// Push runs `git push --porcelain remote refspecs...` and parses the
// per-refspec result, so callers can retry only the rejected refs
// (docs/03-sync-and-concurrency.md「sync のアルゴリズム」).
func (r *Runner) Push(ctx context.Context, remote string, refspecs ...string) ([]PushResult, error) {
	args := append([]string{"push", "--porcelain", remote}, refspecs...)
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	results := parsePushPorcelain(stdout.String())
	if runErr != nil && len(results) == 0 {
		return nil, fmt.Errorf("gitx: git push: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	return results, nil
}

func parsePushPorcelain(out string) []PushResult {
	var results []PushResult
	for _, rawLine := range strings.Split(out, "\n") {
		// NOTE: do not TrimSpace the whole line before reading the status
		// flag - a successful fast-forward update's flag is a literal
		// space (' '), which TrimSpace would eat, making the line look
		// like it starts with the refspec instead and silently
		// misreporting every successful fast-forward push as a failure.
		line := strings.TrimRight(rawLine, "\r")
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "To ") || trimmed == "Done" {
			continue
		}
		code := line[0]
		rest := strings.TrimPrefix(line[1:], "\t")
		fields := strings.Split(rest, "\t")
		refspec := ""
		reason := ""
		if len(fields) > 0 {
			refspec = fields[0]
		}
		if len(fields) > 1 {
			reason = fields[1]
		}
		results = append(results, PushResult{
			Refspec: refspec,
			// git push --porcelain status flags: ' ' fast-forward, '+'
			// forced, '*' new ref, '=' already up to date all succeed;
			// '!' is an error and '-' is a prune (not used by githive).
			OK:     code == ' ' || code == '+' || code == '*' || code == '=',
			Reason: reason,
		})
	}
	return results
}
