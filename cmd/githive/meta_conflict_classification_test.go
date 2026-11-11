package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ymsaki/githive/internal/app/syncapp"
	"github.com/ymsaki/githive/internal/cliout"
	"github.com/ymsaki/githive/internal/core/refspace"
)

func TestClassifyMetaConfigConflictPreservesRecoveryAndPartialResults(t *testing.T) {
	conflict := &syncapp.MetaConfigConflictError{LocalOID: strings.Repeat("a", 40), RemoteOID: strings.Repeat("b", 40)}
	err := &syncFailedError{cause: fmt.Errorf("sync: %w", conflict), results: []syncapp.Result{{Ref: "refs/projects/issue/ok", Action: syncapp.ActionFastForwardOut}}}
	code, info := classifyError(err)
	if code != cliout.ExitVerifyFailed || info.Code != "meta_config_conflict" || info.Retryable {
		t.Fatalf("wrong classification: code=%d info=%+v", code, info)
	}
	checkMetaConflictData(t, info.Data, conflict.LocalOID, conflict.RemoteOID)
	if info.Data["total"] != 1 || len(info.Data["items"].([]any)) != 1 {
		t.Fatalf("partial results lost: %+v", info.Data)
	}
	code, info = classifyError(syncapp.ErrMetaConfigConflict)
	if code != cliout.ExitVerifyFailed || info.Code != "meta_config_conflict" || info.Retryable {
		t.Fatalf("sentinel classification: code=%d info=%+v", code, info)
	}
}

func checkMetaConflictData(t *testing.T, data map[string]any, local, remote string) {
	t.Helper()
	recovery, _ := data["recovery"].(string)
	if data["ref"] != refspace.MetaConfigRef || data["local_oid"] != local || data["remote_oid"] != remote || !strings.Contains(recovery, local) || !strings.Contains(recovery, remote) {
		t.Fatalf("missing conflict recovery metadata: %+v", data)
	}
}

func TestCLIMcpSyncMetaConflictReportsBothImmutableConfigs(t *testing.T) {
	a, b := newCLITestRepo(t), newCLITestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet", "--bare", origin)
	for i, dir := range []string{a, b} {
		mustRunCLI(t, dir, "init", "--project", []string{"remote-project", "local-project"}[i], "--no-sync", "--json")
		git("-C", dir, "remote", "add", "origin", origin)
	}
	git("-C", a, "push", "--quiet", "origin", refspace.MetaConfigRef+":"+refspace.MetaConfigRef)
	local := git("-C", b, "rev-parse", refspace.MetaConfigRef)
	remote := git("-C", origin, "rev-parse", refspace.MetaConfigRef)
	result := runCLI(t, b, "sync", "--json")
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code      string         `json:"code"`
			Retryable bool           `json:"retryable"`
			Data      map[string]any `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("bad JSON: %v %s", err, result.stdout)
	}
	if result.code != cliout.ExitVerifyFailed || payload.OK || payload.Error.Code != "meta_config_conflict" || payload.Error.Retryable {
		t.Fatalf("wrong CLI conflict: %+v %+v", result, payload)
	}
	checkMetaConflictData(t, payload.Error.Data, local, remote)
	session := newMcpTestSession(t, b)
	tool, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "sync", Arguments: map[string]any{}})
	if err != nil || !tool.IsError {
		t.Fatalf("wrong MCP conflict: result=%+v err=%v", tool, err)
	}
	failure := tool.StructuredContent.(map[string]any)["error"].(map[string]any)
	if failure["code"] != "meta_config_conflict" || failure["retryable"] != false {
		t.Fatalf("wrong MCP error metadata: %+v", failure)
	}
	checkMetaConflictData(t, failure["data"].(map[string]any), local, remote)
	if got := git("-C", b, "rev-parse", refspace.MetaConfigRef); got != local {
		t.Fatalf("local config changed: got %s want %s", got, local)
	}
	if got := git("-C", origin, "rev-parse", refspace.MetaConfigRef); got != remote {
		t.Fatalf("remote config changed: got %s want %s", got, remote)
	}
}
