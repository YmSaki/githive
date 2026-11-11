package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/cliout"
)

func trustPublicationFixture(t *testing.T) (dir, origin, localRoot, remoteTip string) {
	t.Helper()
	dir = newCLITestRepo(t)
	other := newCLITestRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitMust(t, dir, "init", "--bare", origin)
	for _, repo := range []string{dir, other} {
		gitMust(t, repo, "remote", "add", "origin", origin)
		gitMust(t, repo, "config", "core.logAllRefUpdates", "always")
	}
	mustRunCLI(t, other, "users", "add", "alice", "--email", "alice@example.com", "--role", "admin", "--no-sync", "--json")
	gitMust(t, other, "push", "origin", "refs/projects/users/registry")
	mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--role", "admin", "--no-sync", "--json")
	return dir, origin, gitMust(t, dir, "rev-parse", "refs/projects/users/registry"), gitMust(t, origin, "rev-parse", "refs/projects/users/registry")
}

func jsonPayload(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("bad JSON %v: %s", err, stdout)
	}
	return m
}

func TestAutoSyncReportsReplacedLocalWriteAndSafeRecovery(t *testing.T) {
	dir, _, _, remote := trustPublicationFixture(t)
	gitMust(t, dir, "config", "githive.trust.root", remote)
	res := runCLI(t, dir, "users", "add", "dave", "--email", "dave@example.com", "--json")
	payload := jsonPayload(t, res.stdout)
	if res.code != 1 || payload["ok"] != false {
		t.Fatalf("discarded write reported success: %+v", res)
	}
	failure := payload["error"].(map[string]any)
	if failure["code"] != "local_write_replaced" || failure["retryable"] != false {
		t.Fatalf("wrong failure: %v", failure)
	}
	data := failure["data"].(map[string]any)
	local := data["local_oid"].(string)
	if data["local_written"] != true || data["local_write_preserved"] != false || data["published"] != false || data["remote_oid"] != remote {
		t.Fatalf("lost publication facts: %v", data)
	}
	if !strings.Contains(gitMust(t, dir, "show", "-s", "--format=%B", local), `"username": "dave"`) {
		t.Fatal("local_oid does not name committed write")
	}
	if gitMust(t, dir, "rev-parse", "refs/projects/users/registry") != remote {
		t.Fatal("pin resolution changed")
	}
	if !strings.Contains(gitMust(t, dir, "reflog", "show", "--format=%H", "refs/projects/users/registry"), local) {
		t.Fatal("existing reflog recovery was broken")
	}
	recovery := data["recovery"].(string)
	savedRef := data["backup_ref"].(string)
	if savedRef != "refs/githive-recovery/users/"+local || !strings.Contains(recovery, savedRef) {
		t.Fatalf("missing durable recovery ref: %s %s", savedRef, recovery)
	}
	if gitMust(t, dir, "rev-parse", savedRef) != local {
		t.Fatal("backup does not preserve write")
	}
	branch := "githive-recovered-users-" + local[:12]
	gitMust(t, dir, "branch", branch, savedRef)
	if gitMust(t, dir, "rev-parse", branch) != local || gitMust(t, dir, "rev-parse", "refs/projects/users/registry") != remote {
		t.Fatal("inspection recovery changed authoritative registry")
	}
	if out, err := exec.Command("git", "-C", dir, "branch", branch, savedRef).CombinedOutput(); err == nil {
		t.Fatalf("recovery overwrites existing branch: %s", out)
	}
	if len(data["warnings"].([]any)) == 0 || len(data["items"].([]any)) == 0 {
		t.Fatal("resolution warnings/partial results were lost")
	}
}

func TestAutoSyncKeepsSuccessfulResolutionWarnings(t *testing.T) {
	dir, origin, localRoot, remote := trustPublicationFixture(t)
	gitMust(t, dir, "config", "githive.trust.root", localRoot)
	res := mustRunCLI(t, dir, "users", "add", "dave", "--email", "dave@example.com", "--json")
	payload := jsonPayload(t, res.stdout)
	warnings := payload["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("warning dropped: %s", res.stdout)
	}
	data := warnings[0].(map[string]any)["data"].(map[string]any)
	if data["published"] != false || data["local_write_preserved"] != true || data["remote_oid"] != remote {
		t.Fatalf("missing kept-local outcome: %v", data)
	}
	if gitMust(t, dir, "rev-parse", "refs/projects/users/registry") != data["local_oid"] || gitMust(t, origin, "rev-parse", "refs/projects/users/registry") != remote {
		t.Fatal("resolution state does not match receipt")
	}
	if !strings.Contains(gitMust(t, dir, "show", "-s", "--format=%B", data["local_oid"].(string)), `"username": "dave"`) {
		t.Fatal("write was not preserved")
	}
}

func TestMcpSyncRetainsResolutionMetadataWithoutAutoSync(t *testing.T) {
	dir, origin, _, remote := trustPublicationFixture(t)
	session := newMcpTestSession(t, dir)
	callMcpTool(t, session, "users_add", map[string]any{"name": "dave", "email": "dave@example.com"})
	local := gitMust(t, dir, "rev-parse", "refs/projects/users/registry")
	if gitMust(t, origin, "rev-parse", "refs/projects/users/registry") != remote || local == remote {
		t.Fatal("MCP write unexpectedly synced")
	}
	gitMust(t, dir, "config", "githive.trust.root", remote)
	data := callMcpTool(t, session, "sync", map[string]any{"kinds": []string{"users"}})
	item := data["items"].([]any)[0].(map[string]any)
	if item["local_oid"] != local || item["remote_oid"] != remote || item["backup_ref"] != "refs/githive-recovery/users/"+local || item["local_write_preserved"] != false || item["published"] != false || item["recovery"] == "" {
		t.Fatalf("MCP discarded resolution facts: %v", data)
	}
	if len(data["warnings"].([]any)) != 1 {
		t.Fatal("MCP lost successful warning")
	}
}

func TestAutoSyncFailureRetainsPartialDeliveryAndLocalWrite(t *testing.T) {
	dir := newCLITestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitMust(t, dir, "init", "--bare", origin)
	gitMust(t, dir, "remote", "add", "origin", origin)
	created := jsonPayload(t, mustRunCLI(t, dir, "issue", "new", "--title", "initial", "--no-sync", "--json").stdout)
	id := created["data"].(map[string]any)["id"].(string)
	mustRunCLI(t, dir, "notify", "post", "--to", "all", "--title", "pending", "--no-sync", "--json")
	hook := "#!/bin/sh\ncase \"$1\" in refs/projects/notify/stream) exit 1;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "update"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	res := mustRunCLI(t, dir, "issue", "edit", id, "--title", "published", "--json")
	payload := jsonPayload(t, res.stdout)
	warning := payload["warnings"].([]any)[0].(map[string]any)
	if warning["code"] != "sync_failed" {
		t.Fatalf("missing network/partial warning: %v", warning)
	}
	data := warning["data"].(map[string]any)
	if data["local_written"] != true || data["local_write_preserved"] != true || data["published"] != true || data["delivery_complete"] != false {
		t.Fatalf("partial publication was collapsed: %v", data)
	}
	if len(data["items"].([]any)) != 1 {
		t.Fatalf("completed ref result lost: %v", data)
	}
	if gitMust(t, origin, "rev-parse", "refs/projects/issue/"+id) != gitMust(t, dir, "rev-parse", "refs/projects/issue/"+id) {
		t.Fatal("successful issue delivery changed")
	}
	if out, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", "refs/projects/notify/stream").CombinedOutput(); err == nil {
		t.Fatalf("fixture failed to reject notify: %s", out)
	}
}

func TestLocalWriteReplacedClassificationPreservesReceipt(t *testing.T) {
	result := syncapp.Result{Ref: "refs/projects/users/registry", Action: syncapp.ActionAdoptedRemoteTrustRoot, LocalOID: strings.Repeat("1", 40), RemoteOID: strings.Repeat("2", 40), Recovery: "save the local OID"}
	err := &localWriteReplacedError{result: result, results: []syncapp.Result{result}, cause: syncapp.ErrRetriesExhausted, warnings: []cliout.Warning{{Code: "prior", Message: "keep me"}}}
	code, info := classifyError(err)
	if code != 1 || info.Code != "local_write_replaced" || info.Retryable || info.Data["local_oid"] != result.LocalOID || info.Data["local_write_preserved"] != false {
		t.Fatalf("receipt classification changed: %d %+v", code, info)
	}
	if info.Data["warnings"].([]any)[0].(map[string]any)["code"] != "prior" {
		t.Fatal("prior warning lost on terminal outcome")
	}
}
