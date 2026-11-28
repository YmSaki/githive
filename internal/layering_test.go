package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/ymsaki/githive/"

// TestArchitectureLayering checks package-level dependency direction, pure core
// import rules, client->core package allowlist, and sentinel-only selector
// usage for chain/gitx/sign in cmd/githive/root.go. Function-level limits
// inside generally allowed packages (identity/event/refspace) are not parsed
// here and remain a code-review check.
func TestArchitectureLayering(t *testing.T) {
	root := filepath.Clean("..")
	fset := token.NewFileSet()

	// docs/01-architecture.md「依存規則」の許容リストに対応する一般許可パッケージ。
	allowedClientCore := map[string]bool{
		modulePrefix + "internal/core/identity": true,
		modulePrefix + "internal/core/event":    true,
		modulePrefix + "internal/core/refspace": true,
	}

	// cmd/githive/root.go は classifyError でのセンチネルエラー照合に限り
	// chain / gitx / sign の参照を許可する（I/O 呼び出しや他の記号は禁止）。
	sentinelOnlyCoreAliases := map[string]string{
		modulePrefix + "internal/core/chain": "chain",
		modulePrefix + "internal/core/gitx":  "gitx",
		modulePrefix + "internal/core/sign":  "sign",
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

			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", relSlash, err)
			}

			isCore := strings.HasPrefix(relSlash, "internal/core/")
			isApp := strings.HasPrefix(relSlash, "internal/app/")
			isMITClient := strings.HasPrefix(relSlash, "cmd/githive/") ||
				strings.HasPrefix(relSlash, "internal/tui/") ||
				strings.HasPrefix(relSlash, "internal/mcpserv/")
			isClient := strings.HasPrefix(relSlash, "cmd/") ||
				strings.HasPrefix(relSlash, "internal/tui/") ||
				strings.HasPrefix(relSlash, "internal/mcpserv/") ||
				strings.HasPrefix(relSlash, "internal/forge/")

			sentinelIdents := map[string]bool{}

			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("unquote import in %s: %v", relSlash, err)
				}

				if isCore {
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
				}

				if isApp {
					if strings.HasPrefix(imp, modulePrefix+"cmd/") ||
						strings.HasPrefix(imp, modulePrefix+"internal/forge") {
						t.Errorf("%s: app layer must not import %s", relSlash, imp)
					}
				}

				if isMITClient {
					if strings.HasPrefix(imp, modulePrefix+"internal/forge") {
						t.Errorf("%s: MIT client must not import %s (ADR-0019)", relSlash, imp)
					}
				}

				if isClient && strings.HasPrefix(imp, modulePrefix+"internal/core/") {
					if allowedClientCore[imp] {
						continue
					}
					if defaultIdent, ok := sentinelOnlyCoreAliases[imp]; ok && relSlash == "cmd/githive/root.go" {
						ident := defaultIdent
						if spec.Name != nil {
							ident = spec.Name.Name
						}
						if ident == "." || ident == "_" {
							t.Errorf("%s: dot/blank import of %s is forbidden", relSlash, imp)
						} else {
							sentinelIdents[ident] = true
						}
						continue
					}
					t.Errorf("%s: client layer must not directly import %s (use internal/app or update docs/01-architecture.md allowlist)", relSlash, imp)
				}
			}

			if len(sentinelIdents) > 0 {
				ast.Inspect(f, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkgIdent, ok := sel.X.(*ast.Ident)
					if !ok || !sentinelIdents[pkgIdent.Name] {
						return true
					}
					sym := sel.Sel.Name
					if !strings.HasPrefix(sym, "Err") && !strings.HasSuffix(sym, "Error") {
						t.Errorf("%s: sentinel-only core package %s.%s must only reference Err* or *Error", relSlash, pkgIdent.Name, sym)
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
}
