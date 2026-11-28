package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/ymsaki/githive/"

// TestArchitectureLayering enforces the dependency direction and client->core
// allowlist defined in docs/01-architecture.md and .claude/rules/go-layering.md.
func TestArchitectureLayering(t *testing.T) {
	root := filepath.Clean("..")
	fset := token.NewFileSet()

	allowedClientCore := map[string]bool{
		modulePrefix + "internal/core/identity": true,
		modulePrefix + "internal/core/event":    true,
		modulePrefix + "internal/core/refspace": true,
	}

	// cmd/githive/root.go imports chain/gitx/sign solely for sentinel error
	// mapping in classifyError (docs/01-architecture.md「依存規則」).
	allowedRootSentinelCore := map[string]bool{
		modulePrefix + "internal/core/chain": true,
		modulePrefix + "internal/core/gitx":  true,
		modulePrefix + "internal/core/sign":  true,
	}

	walkDirs := []string{
		filepath.Join(root, "internal"),
		filepath.Join(root, "cmd"),
	}

	for _, base := range walkDirs {
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relSlash := filepath.ToSlash(rel)

			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", relSlash, err)
			}

			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("unquote import in %s: %v", relSlash, err)
				}

				switch {
				case strings.HasPrefix(relSlash, "internal/core/"):
					if strings.HasPrefix(imp, modulePrefix+"internal/app/") ||
						strings.HasPrefix(imp, modulePrefix+"cmd/") ||
						strings.HasPrefix(imp, modulePrefix+"internal/forge") {
						t.Errorf("%s: core layer must not import %s", relSlash, imp)
					}
					if strings.HasPrefix(relSlash, "internal/core/materialize/") ||
						strings.HasPrefix(relSlash, "internal/core/merge/") {
						if imp == modulePrefix+"internal/core/gitx" ||
							imp == modulePrefix+"internal/core/chain" ||
							strings.HasPrefix(imp, "github.com/go-git/go-git/") {
							t.Errorf("%s: pure core package must not import git I/O package %s", relSlash, imp)
						}
					}

				case strings.HasPrefix(relSlash, "internal/app/"):
					if strings.HasPrefix(imp, modulePrefix+"cmd/") ||
						strings.HasPrefix(imp, modulePrefix+"internal/forge") {
						t.Errorf("%s: app layer must not import %s", relSlash, imp)
					}

				case strings.HasPrefix(relSlash, "cmd/"):
					if strings.HasPrefix(imp, modulePrefix+"internal/core/") {
						if allowedClientCore[imp] {
							continue
						}
						if relSlash == "cmd/githive/root.go" && allowedRootSentinelCore[imp] {
							continue
						}
						t.Errorf("%s: client layer must not directly import %s (use internal/app or update docs/01-architecture.md allowlist)", relSlash, imp)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
}
