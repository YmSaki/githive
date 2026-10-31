package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/event"
)

// TestCLIWritesCurrentSchemaVersion checks the write side of ADR-0015:
// after raising event.SchemaVersion, the events githive actually commits
// must carry the new version rather than the "v": 1 that used to be
// hardcoded in each app service.
func TestCLIWritesCurrentSchemaVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	mustRunCLI(t, dir, "init", "--project", "versions", "--json")
	mustRunCLI(t, dir, "task", "new", "--title", "t", "--no-sync", "--json")
	mustRunCLI(t, dir, "issue", "new", "--title", "i", "--no-sync", "--json")
	mustRunCLI(t, dir, "chat", "new", "--title", "c", "-m", "hello", "--no-sync", "--json")

	refs := gitRefs(t, dir)
	if len(refs) == 0 {
		t.Fatal("no refs/projects refs were created")
	}
	want := `"v": ` + strconv.Itoa(event.SchemaVersion)
	for _, ref := range refs {
		out, err := exec.Command("git", "-C", dir, "log", "--format=%B", ref).Output()
		if err != nil {
			t.Fatalf("git log %s: %v", ref, err)
		}
		body := string(out)
		if !strings.Contains(body, want) {
			t.Errorf("ref %s: no event carries %s:\n%s", ref, want, body)
		}
		if strings.Contains(body, `"v": 1,`) {
			t.Errorf("ref %s: still writing v:1 events:\n%s", ref, body)
		}
	}
}

// TestCLISchemaVersionRejectsReadExit5 fixes ADR-0018's read side: a read
// folds events too, so `issue show` against a repository whose schema_version
// is newer than this build must stop with exit 5 rather than render a state
// under fold semantics the writers did not use.
func TestCLISchemaVersionRejectsReadExit5(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)
	mustRunCLI(t, dir, "init", "--project", "p", "--json")
	id := createdID(t, mustRunCLI(t, dir, "issue", "new", "--title", "t", "--no-sync", "--json").stdout)

	bumpMetaConfigSchemaVersion(t, dir, 999)

	res := runCLI(t, dir, "issue", "show", id, "--no-sync", "--json")
	assertSchemaUnsupported(t, res)
}

// TestCLISyncRefusesNewerRemoteSchema fixes ADR-0018's sync side: when the
// remote's meta/config declares a schema_version newer than this build, sync
// must refuse before merging anything - in particular it must not carry the
// newer config.json into the local meta/config through the meta merge.
func TestCLISyncRefusesNewerRemoteSchema(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	for _, dir := range []string{a, b} {
		if out, err := exec.Command("git", "clone", "--quiet", origin, dir).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		setCLIIdentity(t, dir, filepath.Base(dir)+"@example.com")
	}

	mustRunCLI(t, a, "init", "--project", "shared", "--json")
	mustRunCLI(t, a, "issue", "new", "--title", "t", "--json")
	mustRunCLI(t, a, "sync", "--json")
	mustRunCLI(t, b, "sync", "--json")

	bumpMetaConfigSchemaVersion(t, a, 999)
	if out, err := exec.Command("git", "-C", a, "push", "--quiet", "origin", "refs/projects/meta/config:refs/projects/meta/config").CombinedOutput(); err != nil {
		t.Fatalf("push bumped meta/config: %v\n%s", err, out)
	}

	res := runCLI(t, b, "sync", "--json")
	assertSchemaUnsupported(t, res)

	out, err := exec.Command("git", "-C", b, "show", "refs/projects/meta/config:config.json").CombinedOutput()
	if err != nil {
		t.Fatalf("git show local config.json: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "999") {
		t.Errorf("sync merged the too-new remote config into the local ref:\n%s", out)
	}
}

func createdID(t *testing.T, stdout string) string {
	t.Helper()
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("no id in response (%v):\n%s", err, stdout)
	}
	return created.Data.ID
}

func assertSchemaUnsupported(t *testing.T, res cliResult) {
	t.Helper()
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
