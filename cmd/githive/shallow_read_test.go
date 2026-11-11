package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/usersapp"
)

func TestShallowReadViewsAndHistoryRefusal(t *testing.T) {
	ctx := context.Background()
	source := newCLITestRepo(t)
	origin := filepath.Join(t.TempDir(), "remote.git")
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(source, "init", "--bare", origin)
	git(source, "commit", "--allow-empty", "-m", "seed")
	git(source, "branch", "-M", "main")
	git(source, "commit", "--allow-empty", "-m", "second")
	git(source, "remote", "add", "origin", origin)
	git(source, "push", "origin", "main")
	git(origin, "symbolic-ref", "HEAD", "refs/heads/main")
	data := func(dir string, args ...string) map[string]any {
		t.Helper()
		args = append(args, "--no-sync", "--json")
		res := mustRunCLI(t, dir, args...)
		var out struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	issue := data(source, "issue", "new", "--title", "Issue", "--body", "exact body")["id"].(string)
	data(source, "issue", "comment", issue, "-m", "x")
	data(source, "issue", "comment", issue, "-m", "x\n")
	task := data(source, "task", "new", "--title", "Task", "--body", "task body\n")["id"].(string)
	data(source, "task", "note", task, "-m", "note")
	data(source, "task", "status", task, "doing")
	chat := data(source, "chat", "new", "--title", "Chat", "-m", "first")["id"].(string)
	data(source, "chat", "post", chat, "-m", "second\n")
	data(source, "users", "add", "cli", "--email", "cli@example.com", "--role", "admin")
	data(source, "users", "add", "bob", "--email", "bob@example.com")
	data(source, "issue", "assign", issue, "--add", "cli")
	readID := data(source, "notify", "post", "--title", "read", "--to", "all")["id"].(string)
	data(source, "notify", "post", "--title", "unread", "--to", "all")
	data(source, "notify", "ack", readID)
	git(source, "push", "origin", "refs/projects/*:refs/projects/*")
	clone := func(name string, shallowProjects bool) string {
		dir := filepath.Join(t.TempDir(), name)
		git(source, "clone", "--depth", "1", "file://"+origin, dir)
		if shallowProjects {
			git(dir, "fetch", "--depth", "1", "origin", "+refs/projects/*:refs/projects/*")
		} else {
			git(dir, "fetch", "origin", "+refs/projects/*:refs/projects/*")
		}
		git(dir, "config", "user.email", "cli@example.com")
		git(dir, "config", "user.name", "CLI")
		return dir
	}
	shallow := clone("shallow", true)
	// The read path is local: remove the remote so a hidden deepen cannot help.
	git(shallow, "remote", "remove", "origin")
	for _, args := range [][]string{{"issue", "list"}, {"task", "list"}, {"chat", "list"}, {"users", "list"}, {"notify", "list", "--unread"}, {"status"}, {"whoami"}} {
		shown := data(shallow, args...)
		if shown["read_source"] != "snapshot" || shown["history_complete"] != false || shown["verified"] != false {
			t.Fatalf("%v: missing snapshot boundary: %v", args, shown)
		}
	}
	if data(shallow, "issue", "list", "--assignee", "cli")["total"] != float64(1) || data(shallow, "task", "list", "--owner", "cli")["total"] != float64(1) {
		t.Fatal("view filters used strict mutation resolver")
	}
	if empty := data(shallow, "issue", "list", "--status", "closed"); empty["total"] != float64(0) || empty["read_source"] != "snapshot" {
		t.Fatalf("empty filtered view lost completeness provenance: %v", empty)
	}
	if data(shallow, "whoami")["registered"] != true {
		t.Fatal("shallow registered identity was hidden")
	}
	if data(shallow, "notify", "list", "--unread")["total"] != float64(1) {
		t.Fatal("snapshot acks did not preserve unread count")
	}
	if data(shallow, "status")["unread_notify"] != float64(1) {
		t.Fatal("status lost snapshot unread count")
	}
	for _, tc := range []struct {
		feature, id, col string
		body             string
	}{{"issue", issue, "comments", "exact body"}, {"task", task, "notes", "task body\n"}, {"chat", chat, "messages", ""}} {
		shown := data(shallow, tc.feature, "show", tc.id)
		if shown["read_source"] != "snapshot" || shown["history_complete"] != false || shown["verified"] != false {
			t.Fatalf("show lacks provenance: %v", shown)
		}
		if tc.feature != "chat" && shown["body"] != tc.body {
			t.Fatalf("body.md was normalized: %q", shown["body"])
		}
		items := shown[tc.col].([]any)
		if len(items) == 0 {
			t.Fatal("projection collection disappeared")
		}
		for _, raw := range items {
			m := raw.(map[string]any)
			if m["body_source"] != "rendered_markdown" || !strings.HasSuffix(m["body"].(string), "\n") {
				t.Fatalf("not an explicit persisted rendering: %v", m)
			}
		}
		if tc.feature == "issue" && items[0].(map[string]any)["body"] != items[1].(map[string]any)["body"] {
			t.Fatal("legacy indistinguishable LF projection was fabricated")
		}
	}
	// Strict app APIs used by resolvers and writers do not trust the view.
	if _, err := issueapp.New(shallow).Show(ctx, issue); err == nil {
		t.Fatal("strict Show used snapshot")
	}
	if _, _, _, err := usersapp.New(shallow).List(ctx); err == nil {
		t.Fatal("strict registry List used snapshot")
	}
	if _, err := usersapp.New(shallow).ResolveToEmail(ctx, "cli"); err == nil {
		t.Fatal("mutation resolver used snapshot")
	}
	before := git(shallow, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/")
	for _, args := range [][]string{{"issue", "comment", issue, "-m", "blocked"}, {"issue", "new", "--title", "blocked"}, {"log"}, {"verify"}, {"fsck"}, {"fsck", "--compact"}} {
		args = append(args, "--no-sync", "--json")
		res := runCLI(t, shallow, args...)
		if res.code != 5 || !strings.Contains(res.stdout, `"code": "shallow_history"`) || !strings.Contains(res.stdout, "--unshallow") {
			t.Fatalf("incomplete history did not yield actionable error: %v %+v", args, res)
		}
	}
	if git(shallow, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/") != before {
		t.Fatal("refusal changed project refs")
	}
	session := newMcpTestSession(t, shallow)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{{"issue_show", map[string]any{"id": issue}}, {"task_show", map[string]any{"id": task}}, {"chat_show", map[string]any{"id": chat}}, {"users_list", map[string]any{}}, {"notify_list", map[string]any{"unread": true}}, {"status", map[string]any{}}, {"whoami", map[string]any{}}} {
		shown := callMcpTool(t, session, tc.name, tc.args)
		if shown["read_source"] != "snapshot" || shown["verified"] != false {
			t.Fatalf("MCP omitted boundary: %s %v", tc.name, shown)
		}
	}
	resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "githive://issue/" + issue})
	if err != nil {
		t.Fatal(err)
	}
	var resourceData map[string]any
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &resourceData); err != nil || resourceData["read_source"] != "snapshot" {
		t.Fatalf("resource omitted boundary: %v %v", resourceData, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_new", Arguments: map[string]any{"title": "blocked MCP"}})
	if err == nil && !result.IsError {
		t.Fatal("MCP mutation trusted snapshot")
	}
	if git(shallow, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/") != before {
		t.Fatal("MCP changed shallow project refs")
	}
	// A shallow code branch alone is no restriction on complete project refs.
	complete := clone("complete-projects", false)
	shown := data(complete, "issue", "show", issue)
	if _, ok := shown["read_source"]; ok {
		t.Fatal("complete project was labelled shallow due to source branch")
	}
	data(complete, "issue", "comment", issue, "-m", "complete history write")
}

func TestReadViewDoesNotFallbackOnOrdinaryCorruption(t *testing.T) {
	dir := newCLITestRepo(t)
	ctx := context.Background()
	svc := issueapp.New(dir)
	id, err := svc.NewIssue(ctx, "valid", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Comment(ctx, id, "comment", "", ""); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "refs/projects/issue/"+id+"^").Output()
	if err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(string(out))
	if err := os.Remove(filepath.Join(dir, ".git", "objects", oid[:2], oid[2:])); err != nil {
		t.Fatal(err)
	}
	if shown, err := svc.ShowView(ctx, id); err == nil || shown != nil {
		t.Fatalf("ordinary corruption became an unverified read: %v %v", shown, err)
	}
}
