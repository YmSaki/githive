package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

type relatedFixture struct {
	dir, issue, otherIssue, task, otherTask, chat, otherChat, legacy     string
	comment, comment2, foreignComment, message, message2, foreignMessage string
}

func relatedID(prefix, tail string) string { return prefix + strings.Repeat(tail, 26-len(prefix)) }

func newRelatedFixture(t *testing.T) relatedFixture {
	t.Helper()
	f := relatedFixture{dir: newCLITestRepo(t), issue: relatedID("01j8a000", "0"), otherIssue: relatedID("01j8d000", "4"), task: relatedID("01j8b000", "1"), otherTask: relatedID("01j8b000", "2"), chat: relatedID("01j8e000", "5"), otherChat: relatedID("01j8f000", "6"), comment: relatedID("01j9a000", "1"), comment2: relatedID("01j9a000", "2"), foreignComment: relatedID("01j9b000", "3"), message: relatedID("01j9c000", "4"), message2: relatedID("01j9c000", "5"), foreignMessage: relatedID("01j9d000", "6"), legacy: relatedID("h1j8y000", "7")}
	setCLIIdentity(t, f.dir, "author@example.com")
	appendEvent := func(feature, id, eid, kind string, data map[string]any) {
		var registry *materialize.Registry
		var tree func(*materialize.State) (map[string][]byte, error)
		switch feature {
		case "issue":
			registry, tree = materialize.IssueRegistry, issueapp.TreeFiles
		case "task":
			registry, tree = materialize.TaskRegistry, taskapp.TreeFiles
		case "chat":
			registry, tree = materialize.ChatRegistry, chatapp.TreeFiles
		}
		w := &entitychain.Writer{Dir: f.dir, RefFor: func(string) plumbing.ReferenceName {
			return plumbing.ReferenceName("refs/projects/" + feature + "/" + id)
		}, Registry: registry, TreeFiles: tree}
		_, err := w.Append(context.Background(), id, func() (*event.Envelope, string) {
			return &event.Envelope{V: event.SchemaVersion, Kind: kind, ID: eid, TS: "2026-07-04T00:00:00.000Z", Entity: id, Data: data}, "fixture"
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, entity := range []struct{ feature, id string }{{"issue", f.issue}, {"issue", f.otherIssue}, {"task", f.task}, {"task", f.otherTask}, {"chat", f.chat}, {"chat", f.otherChat}, {"issue", f.legacy}} {
		appendEvent(entity.feature, entity.id, entity.id, entity.feature+".create", map[string]any{"title": "fixture"})
	}
	for _, comment := range []struct{ entity, id string }{{f.issue, f.comment}, {f.issue, f.comment2}, {f.otherIssue, f.foreignComment}} {
		appendEvent("issue", comment.entity, comment.id, "issue.comment", map[string]any{"body": "original"})
	}
	for _, message := range []struct{ entity, id string }{{f.chat, f.message}, {f.chat, f.message2}, {f.otherChat, f.foreignMessage}} {
		appendEvent("chat", message.entity, message.id, "chat.post", map[string]any{"body": "original"})
	}
	setCLIIdentity(t, f.dir, "actor@example.com")
	return f
}

func TestCLIAndMcpResolveRelatedIDsBeforeWriting(t *testing.T) {
	f := newRelatedFixture(t)
	session := newMcpTestSession(t, f.dir)
	// Links use one identity for prefix/full/uppercase add and remove.
	mustRunCLI(t, f.dir, "issue", "link", strings.ToUpper(f.issue), strings.ToUpper(f.task[:12]), "--rel", "task", "--no-sync", "--json")
	callMcpTool(t, session, "issue_link", map[string]any{"id": strings.ToUpper(f.issue[:12]), "linked_id": strings.ToUpper(f.task), "rel": "task"})
	shown, err := issueapp.New(f.dir).Show(context.Background(), f.issue)
	if err != nil {
		t.Fatal(err)
	}
	links := shown.Meta["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["id"] != f.task {
		t.Fatalf("prefix/full links diverged: %+v", links)
	}
	mustRunCLI(t, f.dir, "issue", "link", f.issue, strings.ToUpper(f.task[:12]), "--rel", "task", "--remove", "--no-sync", "--json")
	shown, err = issueapp.New(f.dir).Show(context.Background(), f.issue)
	if err != nil || len(shown.Meta["links"].([]any)) != 0 {
		t.Fatalf("prefix remove failed: %+v %v", shown, err)
	}
	mustRunCLI(t, f.dir, "task", "link", strings.ToUpper(f.task), strings.ToUpper(f.issue[:12]), "--rel", "issue", "--no-sync", "--json")
	callMcpTool(t, session, "task_link", map[string]any{"id": strings.ToUpper(f.task[:12]), "linked_id": strings.ToUpper(f.issue), "rel": "issue", "remove": true})
	task, err := taskapp.New(f.dir).Show(context.Background(), f.task)
	if err != nil || len(task.Meta["links"].([]any)) != 0 {
		t.Fatalf("MCP full remove failed: %+v %v", task, err)
	}
	// Reply and replacement references resolve against this entity's events.
	for _, surface := range []string{"cli", "mcp"} {
		if surface == "cli" {
			mustRunCLI(t, f.dir, "issue", "comment", strings.ToUpper(f.issue[:12]), "-m", "cli reply", "--reply-to", strings.ToUpper(f.comment[:12]), "--supersedes", strings.ToUpper(f.comment2[:12]), "--no-sync", "--json")
			mustRunCLI(t, f.dir, "chat", "post", strings.ToUpper(f.chat[:12]), "-m", "cli message", "--reply-to", strings.ToUpper(f.message[:12]), "--supersedes", strings.ToUpper(f.message2[:12]), "--no-sync", "--json")
			mustRunCLI(t, f.dir, "notify", "post", "--to", "all", "--title", "cli source", "--source", "task:"+strings.ToUpper(f.task[:12]), "--no-sync", "--json")
		} else {
			callMcpTool(t, session, "issue_comment", map[string]any{"id": strings.ToUpper(f.issue), "message": "mcp reply", "reply_to": strings.ToUpper(f.comment), "supersedes": strings.ToUpper(f.comment2)})
			callMcpTool(t, session, "chat_post", map[string]any{"id": strings.ToUpper(f.chat), "body": "mcp message", "reply_to": strings.ToUpper(f.message), "supersedes": strings.ToUpper(f.message2)})
			callMcpTool(t, session, "notify_post", map[string]any{"to": []string{"all"}, "title": "mcp source", "source": map[string]any{"kind": "chat", "id": strings.ToUpper(f.chat[:12])}})
		}
	}
	shown, err = issueapp.New(f.dir).Show(context.Background(), f.issue)
	if err != nil {
		t.Fatal(err)
	}
	replies := 0
	for _, comment := range shown.Comments {
		if comment["body"] == "cli reply" || comment["body"] == "mcp reply" {
			replies++
			if comment["reply_to"] != f.comment || comment["supersedes"] != f.comment2 {
				t.Fatalf("unresolved comment: %+v", comment)
			}
		}
	}
	if replies != 2 {
		t.Fatalf("missing replies: %+v", shown.Comments)
	}
	chat, err := chatapp.New(f.dir).Show(context.Background(), f.chat)
	if err != nil {
		t.Fatal(err)
	}
	messages := 0
	for _, message := range chat.Messages {
		if message["body"] == "cli message" || message["body"] == "mcp message" {
			messages++
			if message["reply_to"] != f.message || message["supersedes"] != f.message2 {
				t.Fatalf("unresolved message: %+v", message)
			}
		}
	}
	if messages != 2 {
		t.Fatalf("missing messages: %+v", chat.Messages)
	}
	posts, err := notifyapp.New(f.dir).List(context.Background(), notifyapp.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	notified := 0
	sources := 0
	for _, post := range posts {
		source, _ := post["source"].(map[string]any)
		switch post["title"] {
		case "cli source":
			sources++
			if source["id"] != f.task {
				t.Fatalf("CLI source unresolved: %+v", post)
			}
		case "mcp source":
			sources++
			if source["id"] != f.chat {
				t.Fatalf("MCP source unresolved: %+v", post)
			}
		default:
			if strings.Contains(post["title"].(string), "コメントが返信") {
				notified++
				if source["id"] != f.issue {
					t.Fatalf("reply source: %+v", post)
				}
			}
		}
	}
	if notified != 2 || sources != 2 {
		t.Fatalf("short reply failed to notify author or source missing: notified=%d sources=%d posts=%+v", notified, sources, posts)
	}
}

func TestCLIAndMcpRejectUnresolvedRelatedIDsWithoutAppending(t *testing.T) {
	f := newRelatedFixture(t)
	session := newMcpTestSession(t, f.dir)
	refs := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", f.dir, "for-each-ref", "--format=%(refname) %(objectname)").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	unknown := relatedID("01j7z000", "0")
	cases := []struct {
		name       string
		cli        []string
		tool       string
		args       map[string]any
		code       int
		errorCode  string
		candidates []string
	}{
		{"ambiguous task link", []string{"issue", "link", f.issue, f.task[:8], "--rel", "task"}, "issue_link", map[string]any{"id": f.issue, "linked_id": f.task[:8], "rel": "task"}, 2, "ambiguous_id", []string{f.task, f.otherTask}},
		{"unknown link prefix", []string{"task", "link", f.task, unknown[:12], "--rel", "issue"}, "task_link", map[string]any{"id": f.task, "linked_id": unknown[:12], "rel": "issue"}, 1, "not_found", nil},
		{"wrong source feature", []string{"notify", "post", "--to", "all", "--title", "bad", "--source", "issue:" + f.task[:12]}, "notify_post", map[string]any{"to": []string{"all"}, "title": "bad", "source": map[string]any{"kind": "issue", "id": f.task[:12]}}, 1, "not_found", nil},
		{"ambiguous source", []string{"notify", "post", "--to", "all", "--title", "bad", "--source", "task:" + f.task[:8]}, "notify_post", map[string]any{"to": []string{"all"}, "title": "bad", "source": map[string]any{"kind": "task", "id": f.task[:8]}}, 2, "ambiguous_id", []string{f.task, f.otherTask}},
		{"ambiguous comment", []string{"issue", "comment", f.issue, "-m", "bad", "--reply-to", f.comment[:8]}, "issue_comment", map[string]any{"id": f.issue, "message": "bad", "reply_to": f.comment[:8]}, 2, "ambiguous_id", []string{f.comment, f.comment2}},
		{"foreign comment", []string{"issue", "comment", f.issue, "-m", "bad", "--reply-to", f.foreignComment}, "issue_comment", map[string]any{"id": f.issue, "message": "bad", "reply_to": f.foreignComment}, 1, "not_found", nil},
		{"wrong event kind", []string{"issue", "comment", f.issue, "-m", "bad", "--supersedes", f.issue}, "issue_comment", map[string]any{"id": f.issue, "message": "bad", "supersedes": f.issue}, 1, "not_found", nil},
		{"ambiguous chat", []string{"chat", "post", f.chat, "-m", "bad", "--supersedes", f.message[:8]}, "chat_post", map[string]any{"id": f.chat, "body": "bad", "supersedes": f.message[:8]}, 2, "ambiguous_id", []string{f.message, f.message2}},
		{"foreign message", []string{"chat", "post", f.chat, "-m", "bad", "--supersedes", f.foreignMessage}, "chat_post", map[string]any{"id": f.chat, "body": "bad", "supersedes": f.foreignMessage}, 1, "not_found", nil},
		{"unknown reply", []string{"issue", "comment", f.issue, "-m", "bad", "--reply-to", unknown}, "issue_comment", map[string]any{"id": f.issue, "message": "bad", "reply_to": unknown}, 1, "not_found", nil},
		{"opaque source prefix", []string{"notify", "post", "--to", "all", "--title", "bad", "--source", "external:" + f.issue[:12]}, "notify_post", map[string]any{"to": []string{"all"}, "title": "bad", "source": map[string]any{"kind": "external", "id": f.issue[:12]}}, 2, "invalid_input", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := refs()
			result := runCLI(t, f.dir, append(tc.cli, "--no-sync", "--json")...)
			var payload struct {
				Error struct {
					Code string
					Data struct{ Candidates []string }
				}
			}
			if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
				t.Fatal(err)
			}
			if result.code != tc.code || payload.Error.Code != tc.errorCode {
				t.Fatalf("wrong CLI error: %+v", result)
			}
			if len(tc.candidates) > 0 && strings.Join(payload.Error.Data.Candidates, ",") != strings.Join(tc.candidates, ",") {
				t.Fatalf("missing full candidates: %+v", result)
			}
			if refs() != before {
				t.Fatal("failed CLI operation appended events")
			}
			resultMcp, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil || !resultMcp.IsError {
				t.Fatalf("MCP should return tool error: %+v %v", resultMcp, err)
			}
			for _, candidate := range tc.candidates {
				if !strings.Contains(resultMcp.Content[0].(*mcp.TextContent).Text, candidate) {
					t.Fatalf("MCP lost full candidate: %+v", resultMcp)
				}
			}
			if refs() != before {
				t.Fatal("failed MCP operation appended events")
			}
		})
	}
}

func TestRelatedFullWeakReferencesAndOpaqueSourcesRemainSupported(t *testing.T) {
	f := newRelatedFixture(t)
	// Existing exact full legacy refs keep working even when their first ULID
	// character lies outside the narrower range used by modern generators.
	shown := mustRunCLI(t, f.dir, "issue", "show", strings.ToUpper(f.legacy), "--json")
	if !strings.Contains(shown.stdout, f.legacy) {
		t.Fatalf("legacy exact reference lost: %+v", shown)
	}

	missing := relatedID("h1j8z000", "7") // accepted legacy 26-character ref grammar
	mustRunCLI(t, f.dir, "task", "link", f.task, strings.ToUpper(missing), "--rel", "issue", "--no-sync", "--json")
	session := newMcpTestSession(t, f.dir)
	legacy := callMcpTool(t, session, "issue_show", map[string]any{"id": strings.ToUpper(f.legacy)})
	if legacy["meta"].(map[string]any)["id"] != f.legacy {
		t.Fatalf("MCP legacy full ID lost: %+v", legacy)
	}
	callMcpTool(t, session, "task_link", map[string]any{"id": f.task, "linked_id": strings.ToUpper(missing), "rel": "issue", "remove": true})
	mustRunCLI(t, f.dir, "notify", "post", "--to", "all", "--title", "external cli", "--source", "external:"+strings.ToUpper(missing), "--no-sync", "--json")
	callMcpTool(t, session, "notify_post", map[string]any{"to": []string{"all"}, "title": "external mcp", "source": map[string]any{"kind": "external", "id": strings.ToUpper(missing)}})
	input := map[string]any{"kind": "external", "id": strings.ToUpper(missing), "extension": "retained"}
	if _, err := notifyapp.New(f.dir).Post(context.Background(), []string{"all"}, "extension", "", input, ""); err != nil {
		t.Fatal(err)
	}
	if input["id"] != strings.ToUpper(missing) {
		t.Fatal("source input map was mutated")
	}
	posts, err := notifyapp.New(f.dir).List(context.Background(), notifyapp.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, post := range posts {
		source := post["source"].(map[string]any)
		if source["id"] != missing || source["kind"] != "external" {
			t.Fatalf("opaque source changed: %+v", source)
		}
		if post["title"] == "extension" && source["extension"] != "retained" {
			t.Fatal("source extension lost")
		}
	}
}
