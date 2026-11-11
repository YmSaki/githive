package fsckapp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/initapp"
)

const metaConfigRef = "refs/projects/meta/config"

func gitOut(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// rewriteMetaConfigTree replaces the meta/config head commit with one that
// has the same (valid) envelope message but a tree whose config.json is
// configJSON, or has no config.json at all when configJSON is "". The
// envelope stays valid, so only the config checks can report anything.
func rewriteMetaConfigTree(t *testing.T, dir, configJSON string) {
	t.Helper()
	var entries []string
	for _, line := range strings.Split(gitOut(t, dir, "", "ls-tree", metaConfigRef), "\n") {
		if !strings.HasSuffix(line, "\tconfig.json") {
			entries = append(entries, line)
		}
	}
	if configJSON != "" {
		blob := gitOut(t, dir, configJSON, "hash-object", "-w", "--stdin")
		entries = append(entries, fmt.Sprintf("100644 blob %s\tconfig.json", blob))
	}
	listing := ""
	if len(entries) > 0 {
		listing = strings.Join(entries, "\n") + "\n"
	}
	tree := gitOut(t, dir, listing, "mktree")
	msg := gitOut(t, dir, "", "log", "-1", "--format=%B", metaConfigRef)
	commit := gitOut(t, dir, "", "commit-tree", tree, "-m", msg)
	gitOut(t, dir, "", "update-ref", metaConfigRef, commit)
}

func metaFindingCodes(t *testing.T, dir string) []string {
	t.Helper()
	report, err := New(dir).Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, f := range report.Findings {
		if f.Ref == metaConfigRef {
			codes = append(codes, f.Code)
		}
	}
	return codes
}

// TestMetaConfigChecks covers fsck's meta/config validation
// (docs/01-architecture.md「バージョニングと互換性」): a fresh init is clean,
// a schema_version newer than this build is reported, and a tree without
// config.json is reported.
func TestMetaConfigChecks(t *testing.T) {
	requireGit(t)
	dir := newTestRepo(t)
	if _, err := initapp.Init(context.Background(), dir, "origin", "p"); err != nil {
		t.Fatal(err)
	}
	if codes := metaFindingCodes(t, dir); len(codes) != 0 {
		t.Fatalf("fresh init should be clean, got %v", codes)
	}

	newer := fmt.Sprintf("{\n  \"project\": \"p\",\n  \"schema_version\": %d\n}\n", initapp.SchemaVersion+1)
	rewriteMetaConfigTree(t, dir, newer)
	if codes := metaFindingCodes(t, dir); len(codes) != 1 || codes[0] != "unsupported_schema_version" {
		t.Errorf("expected [unsupported_schema_version], got %v", codes)
	}

	rewriteMetaConfigTree(t, dir, "")
	if codes := metaFindingCodes(t, dir); len(codes) != 1 || codes[0] != "missing_config" {
		t.Errorf("expected [missing_config], got %v", codes)
	}
}
