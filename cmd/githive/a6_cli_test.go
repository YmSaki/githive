package main

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// These e2e tests cover A6 (docs/13-roadmap.md「A6. CLI 小物バグ群」): a
// handful of independent small CLI fixes each verified end-to-end against
// the real binary, the way p4_cli_test.go does for P4.

// TestCLISyncRetriesConfigDoesNotBreakWrites is item 2's CLI-level check:
// root.go's syncIfEnabled now reads githive.sync.retries (via
// syncapp.RetriesFromConfig) instead of ignoring it entirely, and an unset
// remote (a hard sync failure, not a retryable race) still degrades to a
// warning rather than failing the write - for every retries value, valid or
// not (docs/10-cli-spec.md「設定（git config）」「エラー処理方針」). The
// parsing rules themselves (unset/invalid -> DefaultRetries) are unit-tested
// in internal/app/syncapp/syncapp_test.go's TestRetriesFromConfig; this only
// proves the CLI actually wires the config value through instead of
// silently always using the hardcoded default.
func TestCLISyncRetriesConfigDoesNotBreakWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	for _, v := range []string{"2", "0", "not-a-number"} {
		if out, err := exec.Command("git", "-C", dir, "config", "githive.sync.retries", v).CombinedOutput(); err != nil {
			t.Fatalf("git config githive.sync.retries %q: %v\n%s", v, err, out)
		}
		res := runCLI(t, dir, "issue", "new", "--title", "t-"+v, "--json")
		if res.code != 0 {
			t.Fatalf("githive.sync.retries=%q: issue new should still exit 0 (sync failure is a warning), got code=%d stdout=%s stderr=%s", v, res.code, res.stdout, res.stderr)
		}
	}
}

// TestCLIVerifyAllFlag is item 3's CLI-level check: `verify --all` is
// accepted as an explicit alias for the default "verify every ref" behavior
// (docs/10-cli-spec.md「コマンド体系」), and combining it with --ref is a
// usage error (exit code 2).
func TestCLIVerifyAllFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	// No entities are created here on purpose: verifying a real signed
	// commit needs an ssh-keygen + registered signer fixture (see
	// sign_test.go), which is orthogonal to what this test checks (that
	// --all is accepted and mutually exclusive with --ref). An empty repo
	// has zero refs to verify, so `verify --all` trivially succeeds.
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "verify", "--all", "--json")
	if res.code != 0 {
		t.Fatalf("verify --all should exit 0 on an empty repo, got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}

	res = runCLI(t, dir, "verify", "--all", "--ref", "refs/projects/issue/does-not-matter", "--json")
	if res.code != 2 {
		t.Fatalf("verify --all --ref should exit 2 (usage error), got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &failed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if failed.OK || failed.Error.Code != "usage_error" {
		t.Errorf("unexpected failure envelope: %+v", failed)
	}
}

// bumpMetaConfigSchemaVersion rewrites refs/projects/meta/config's tip so
// its config.json declares the given schema_version, keeping everything
// else about the tree shape intact. It uses plain git plumbing rather than
// going through githive (whose write path is exactly what item 4 makes
// reject a too-new schema_version).
func bumpMetaConfigSchemaVersion(t *testing.T, dir string, version int) {
	t.Helper()
	const ref = "refs/projects/meta/config"

	parentOut, err := exec.Command("git", "-C", dir, "rev-parse", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s: %v\n%s", ref, err, parentOut)
	}
	parent := strings.TrimSpace(string(parentOut))

	configJSON := `{"schema_version":` + strconv.Itoa(version) + `,"project":"t","features":["issue","task","notify","users","chat","wiki"],"created_at":"2026-01-01T00:00:00.000Z"}`
	hashCmd := exec.Command("git", "-C", dir, "hash-object", "-w", "--stdin")
	hashCmd.Stdin = strings.NewReader(configJSON)
	blobOut, err := hashCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hash-object: %v\n%s", err, blobOut)
	}
	blob := strings.TrimSpace(string(blobOut))

	mktreeCmd := exec.Command("git", "-C", dir, "mktree")
	mktreeCmd.Stdin = strings.NewReader("100644 blob " + blob + "\tconfig.json\n")
	treeOut, err := mktreeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mktree: %v\n%s", err, treeOut)
	}
	tree := strings.TrimSpace(string(treeOut))

	commitOut, err := exec.Command("git", "-C", dir, "commit-tree", tree, "-p", parent, "-m", "test: bump schema_version").CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v\n%s", err, commitOut)
	}
	commit := strings.TrimSpace(string(commitOut))

	if out, err := exec.Command("git", "-C", dir, "update-ref", ref, commit).CombinedOutput(); err != nil {
		t.Fatalf("update-ref %s %s: %v\n%s", ref, commit, err, out)
	}
}

// TestCLISchemaVersionRejectsWriteExit5 is item 4's headline scenario: a
// repository whose meta/config declares a schema_version newer than this
// build supports must reject writes with exit code 5 and error.code
// "schema_unsupported" (docs/02-data-model.md「ツールは操作前に
// schema_version を確認し、自分の対応バージョンより新しければ操作を拒否す
// る」, docs/10-cli-spec.md「終了コード」5).
func TestCLISchemaVersionRejectsWriteExit5(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	if res := runCLI(t, dir, "init", "--no-sync", "--json"); res.code != 0 {
		t.Fatalf("init failed: code=%d stderr=%s", res.code, res.stderr)
	}

	bumpMetaConfigSchemaVersion(t, dir, 999)

	res := runCLI(t, dir, "issue", "new", "--title", "t", "--no-sync", "--json")
	if res.code != 5 {
		t.Fatalf("expected exit 5, got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	var failed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &failed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if failed.OK || failed.Error.Code != "schema_unsupported" {
		t.Errorf("unexpected failure envelope: %+v", failed)
	}
}

// TestCLISchemaVersionSkippedWithoutMetaConfig verifies the "pre-init /
// partial clone" carve-out (docs/13-roadmap.md A6 item 4「meta/config ref が
// 無いリポジトリ（init 前/部分クローン）はチェックをスキップ（従来動作維
// 持）」): a repo that never ran `githive init` (so refs/projects/meta/config
// does not exist) must still be able to write.
func TestCLISchemaVersionSkippedWithoutMetaConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // no `githive init` here on purpose

	res := runCLI(t, dir, "issue", "new", "--title", "t", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("expected exit 0 (schema check skipped, no meta/config), got code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
}

// TestCLIIssueListExcludesArchivedByDefault is item 1's CLI-level check: an
// archived issue is dropped from `issue list`'s default output (both text
// and --json), and only reappears when `--status archived` is given
// explicitly (docs/features/issue.md「archived は... 一覧の既定表示から外れ
// る」). This is a behavior change: before this fix, List() with no status
// filter returned every issue regardless of status.
func TestCLIIssueListExcludesArchivedByDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "issue", "new", "--title", "open one", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new (open) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var openCreated struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &openCreated); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}

	res = runCLI(t, dir, "issue", "new", "--title", "archived one", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new (archived) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var archivedCreated struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &archivedCreated); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	archivedID := archivedCreated.Data.ID

	for _, to := range []string{"closed", "archived"} {
		if res := runCLI(t, dir, "issue", "status", archivedID, to, "--no-sync", "--json"); res.code != 0 {
			t.Fatalf("issue status -> %s failed: code=%d stderr=%s", to, res.code, res.stderr)
		}
	}

	// Default `--json` output must not include the archived issue.
	res = runCLI(t, dir, "issue", "list", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue list --json failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if listed.Data.Total != 1 || len(listed.Data.Items) != 1 || listed.Data.Items[0]["id"] != openCreated.Data.ID {
		t.Fatalf("expected only the open issue in default --json list, got %+v", listed.Data)
	}

	// Default human-readable output must not mention the archived issue either.
	res = runCLI(t, dir, "issue", "list", "--no-sync")
	if res.code != 0 {
		t.Fatalf("issue list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if strings.Contains(res.stdout, archivedID) {
		t.Errorf("default issue list text output should not mention the archived issue:\n%s", res.stdout)
	}

	// --status archived must show it.
	res = runCLI(t, dir, "issue", "list", "--status", "archived", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue list --status archived failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var archivedListed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &archivedListed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(archivedListed.Data.Items) != 1 || archivedListed.Data.Items[0]["id"] != archivedID {
		t.Fatalf("expected the archived issue when --status archived is given, got %+v", archivedListed.Data)
	}
}
