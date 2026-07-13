// Package wikifs holds the OS-portability rules for wiki file trees that both
// `githive wiki save` and `githive fsck` enforce (docs/features/wiki.md
// 「ページ間リンクと他機能への参照」): a tree authored on one OS must not be
// able to corrupt a checkout on another. It is pure (no I/O), so it lives in
// internal/core and both internal/app/wikiapp and internal/app/fsckapp reuse
// it rather than duplicating the rules (a divergence would let one enforcer
// accept what the other rejects).
package wikifs

import (
	"strconv"
	"strings"
)

// MaxAssetBytes is the size ceiling for a single file under _assets/. Larger
// binaries are rejected until LFS support lands (docs/features/wiki.md; the
// same threshold must be used by wiki save and fsck).
const MaxAssetBytes = 1 << 20 // 1 MiB

// AssetsPrefix is the tree prefix whose files are subject to MaxAssetBytes.
const AssetsPrefix = "_assets/"

// File is one entry in a wiki tree: a slash-separated repo-relative path and
// its blob size in bytes.
type File struct {
	Path string
	Size int64
}

// Violation is one portability problem. Code is a stable machine token (for
// --json and tests); Message is human-facing.
type Violation struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// winReserved is the set of Windows reserved device basenames (compared
// case-insensitively, with or without an extension: "CON" and "con.md" are
// both rejected).
var winReserved = func() map[string]bool {
	m := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}
	for i := 1; i <= 9; i++ {
		m["com"+strconv.Itoa(i)] = true
		m["lpt"+strconv.Itoa(i)] = true
	}
	return m
}()

// invalidRunes are characters that are illegal in path segments on Windows
// (and thus non-portable). '/' is the separator and handled separately.
const invalidRunes = `<>:"\|?*`

// ReservedBaseName reports whether segment's base name (the part before its
// first dot) is a Windows reserved device name.
func ReservedBaseName(segment string) bool {
	base := segment
	if i := strings.IndexByte(segment, '.'); i >= 0 {
		base = segment[:i]
	}
	return winReserved[strings.ToLower(base)]
}

// Check validates a whole wiki tree and returns every violation found:
// per-file rules (Windows reserved names, trailing dot/space, illegal
// characters, control characters, and the _assets/ 1 MiB cap) plus the
// cross-file rule that no two paths may differ only in case (which collide on
// a case-insensitive filesystem). The result is deterministic in input order
// for the per-file checks; collisions are reported on the later path.
func Check(files []File) []Violation {
	var vs []Violation
	seen := make(map[string]string, len(files)) // lowercased path -> first original

	for _, f := range files {
		p := f.Path
		for _, seg := range strings.Split(p, "/") {
			if seg == "" {
				continue
			}
			if trimmed := strings.TrimRight(seg, ". "); trimmed != seg {
				vs = append(vs, Violation{p, "trailing_dot_space",
					"path segment ends with a dot or space (unportable to Windows): " + seg})
			}
			if ReservedBaseName(seg) {
				vs = append(vs, Violation{p, "reserved_name",
					"path segment is a Windows reserved device name: " + seg})
			}
			if i := strings.IndexAny(seg, invalidRunes); i >= 0 {
				vs = append(vs, Violation{p, "invalid_char",
					"path segment contains a character illegal on Windows: " + seg})
			} else if hasControl(seg) {
				vs = append(vs, Violation{p, "invalid_char",
					"path segment contains a control character: " + seg})
			}
		}

		lower := strings.ToLower(p)
		if first, ok := seen[lower]; ok && first != p {
			vs = append(vs, Violation{p, "case_collision",
				"path collides with " + first + " on a case-insensitive filesystem"})
		} else if !ok {
			seen[lower] = p
		}

		if strings.HasPrefix(p, AssetsPrefix) && f.Size > MaxAssetBytes {
			vs = append(vs, Violation{p, "oversize_asset",
				"file under _assets/ exceeds the " + strconv.Itoa(MaxAssetBytes) + "-byte limit (LFS not yet supported)"})
		}
	}
	return vs
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
