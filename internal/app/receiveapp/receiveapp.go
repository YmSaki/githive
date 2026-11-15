// Package receiveapp decides whether a set of pushed ref updates may be
// accepted by a githive-aware server. It implements the parts of the forge
// push pipeline that do not depend on signature/schema verification
// (docs/12-forge-server.md「push パイプライン」 1 and 6): the reserved
// namespace and namespace_mode check, and the fast-forward-only rule for
// refs/projects/**.
//
// The rules are a contract of the data format rather than forge-only
// behaviour, so they live in the MIT app layer where any compatible server
// can reuse them (ADR-0035). The forge's pre-receive hook
// (internal/forge/hook) only does the hook I/O around this package.
//
// Commit reachability is answered by system git through core/gitx, which
// inherits the hook's environment and therefore sees the quarantined objects
// git exposes via GIT_OBJECT_DIRECTORY / GIT_ALTERNATE_OBJECT_DIRECTORIES
// (ADR-0035 決定 1).
package receiveapp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Rejection codes. They are part of the hook's output contract
// (ADR-0035「帰結」).
const (
	CodeReservedNamespace   = "reserved_namespace"
	CodeNamespaceNotAllowed = "namespace_not_allowed"
	CodeNonFastForward      = "non_fast_forward"
)

// NamespaceMode selects how refs outside the reserved namespace are
// treated (docs/12-forge-server.md「push パイプライン」 1).
type NamespaceMode string

const (
	// NamespaceOpen accepts any ref outside the reserved namespace, like
	// GitHub and plain git hosting. It is the default.
	NamespaceOpen NamespaceMode = "open"
	// NamespaceStrict accepts only refs/heads/**, refs/tags/**,
	// refs/projects/** and the configured extra namespaces.
	NamespaceStrict NamespaceMode = "strict"
)

// strictBuiltinNamespaces are always accepted in strict mode.
var strictBuiltinNamespaces = []string{"refs/heads/", "refs/tags/", refspace.Root + "/"}

// ErrInvalidInput reports a malformed pre-receive input line or an invalid
// Config.
var ErrInvalidInput = errors.New("receiveapp: invalid input")

// Config is the server-side policy for namespace acceptance.
type Config struct {
	// Mode is NamespaceOpen when empty.
	Mode NamespaceMode
	// AllowedNamespaces are extra ref prefixes accepted in strict mode
	// (e.g. "refs/notes/"). Each must start with "refs/"; a missing
	// trailing "/" is added. Ignored in open mode.
	AllowedNamespaces []string
}

// RefUpdate is one line of pre-receive input: "<old> <new> <ref>".
type RefUpdate struct {
	Old string
	New string
	Ref string
}

// IsCreate reports whether the update creates Ref.
func (u RefUpdate) IsCreate() bool { return isZeroOID(u.Old) }

// IsDelete reports whether the update deletes Ref.
func (u RefUpdate) IsDelete() bool { return isZeroOID(u.New) }

// Rejection explains why one ref update is refused.
type Rejection struct {
	Ref     string
	Code    string
	Message string
}

// Service checks ref updates against one repository.
type Service struct {
	git *gitx.Runner
}

// New returns a Service for the repository at dir (for a pre-receive hook,
// the repository git runs the hook in).
func New(dir string) *Service {
	return &Service{git: gitx.New(dir)}
}

var oidPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func isZeroOID(oid string) bool {
	return strings.Trim(oid, "0") == ""
}

// ParseUpdates reads pre-receive input (githooks(5): one
// "<old-oid> SP <new-oid> SP <ref-name> LF" per updated ref).
func ParseUpdates(r io.Reader) ([]RefUpdate, error) {
	var updates []RefUpdate
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if text == "" {
			continue
		}
		fields := strings.Split(text, " ")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: line %d: want \"<old> <new> <ref>\"", ErrInvalidInput, line)
		}
		u := RefUpdate{Old: fields[0], New: fields[1], Ref: fields[2]}
		if !oidPattern.MatchString(u.Old) || !oidPattern.MatchString(u.New) {
			return nil, fmt.Errorf("%w: line %d: malformed object id", ErrInvalidInput, line)
		}
		if len(u.Old) != len(u.New) {
			return nil, fmt.Errorf("%w: line %d: object id lengths differ", ErrInvalidInput, line)
		}
		if !strings.HasPrefix(u.Ref, "refs/") {
			return nil, fmt.Errorf("%w: line %d: ref %q is not under refs/", ErrInvalidInput, line, u.Ref)
		}
		updates = append(updates, u)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("receiveapp: read updates: %w", err)
	}
	return updates, nil
}

// normalize validates cfg and returns the effective strict-mode prefixes.
func (cfg Config) normalize() (NamespaceMode, []string, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = NamespaceOpen
	}
	if mode != NamespaceOpen && mode != NamespaceStrict {
		return "", nil, fmt.Errorf("%w: namespace mode %q (want open or strict)", ErrInvalidInput, cfg.Mode)
	}
	prefixes := append([]string(nil), strictBuiltinNamespaces...)
	for _, ns := range cfg.AllowedNamespaces {
		if !strings.HasPrefix(ns, "refs/") || ns == "refs/" {
			return "", nil, fmt.Errorf("%w: allowed namespace %q must be a prefix under refs/", ErrInvalidInput, ns)
		}
		if !strings.HasSuffix(ns, "/") {
			ns += "/"
		}
		if isReserved(ns) || strings.HasPrefix(refspace.RemoteTrackingRoot+"/", ns) {
			return "", nil, fmt.Errorf("%w: allowed namespace %q overlaps the reserved %s/", ErrInvalidInput, ns, refspace.RemoteTrackingRoot)
		}
		prefixes = append(prefixes, ns)
	}
	return mode, prefixes, nil
}

// ValidateConfig reports whether cfg is usable, so a hook can fail with a
// configuration error before reading any input.
func ValidateConfig(cfg Config) error {
	_, _, err := cfg.normalize()
	return err
}

func isReserved(ref string) bool {
	return ref == refspace.RemoteTrackingRoot || strings.HasPrefix(ref, refspace.RemoteTrackingRoot+"/")
}

func isProjects(ref string) bool {
	return strings.HasPrefix(ref, refspace.Root+"/")
}

// Check returns one Rejection per refused update, in ref-name order, or nil
// when every update may be accepted. A non-nil error means the check itself
// could not run (bad config, git failure); callers must then refuse the
// whole push rather than accept it.
func (s *Service) Check(ctx context.Context, cfg Config, updates []RefUpdate) ([]Rejection, error) {
	mode, prefixes, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	var rejections []Rejection
	for _, u := range updates {
		r, err := s.checkOne(ctx, mode, prefixes, u)
		if err != nil {
			return nil, err
		}
		if r != nil {
			rejections = append(rejections, *r)
		}
	}
	sort.SliceStable(rejections, func(i, j int) bool { return rejections[i].Ref < rejections[j].Ref })
	return rejections, nil
}

func (s *Service) checkOne(ctx context.Context, mode NamespaceMode, prefixes []string, u RefUpdate) (*Rejection, error) {
	if isReserved(u.Ref) {
		return &Rejection{
			Ref:     u.Ref,
			Code:    CodeReservedNamespace,
			Message: refspace.RemoteTrackingRoot + "/ is reserved for client-side remote tracking and cannot be pushed",
		}, nil
	}
	if mode == NamespaceStrict && !hasAnyPrefix(u.Ref, prefixes) {
		return &Rejection{
			Ref:     u.Ref,
			Code:    CodeNamespaceNotAllowed,
			Message: "namespace_mode is strict and this ref is outside the accepted namespaces (" + strings.Join(prefixes, ", ") + ")",
		}, nil
	}
	// docs/12「push パイプライン」 6: refs/projects/* は force push を常に拒否。
	// 作成・削除は fast-forward の判定対象外（削除の扱いは ADR-0035 決定 3）。
	if isProjects(u.Ref) && !u.IsCreate() && !u.IsDelete() {
		ok, err := s.git.IsAncestor(ctx, u.Old, u.New)
		if err != nil {
			return nil, fmt.Errorf("receiveapp: fast-forward check for %s: %w", u.Ref, err)
		}
		if !ok {
			return &Rejection{
				Ref:     u.Ref,
				Code:    CodeNonFastForward,
				Message: "refs under " + refspace.Root + "/ only accept fast-forward updates; fetch, let `githive sync` merge, and push again",
			}, nil
		}
	}
	return nil, nil
}

func hasAnyPrefix(ref string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}
