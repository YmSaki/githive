package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func seedDisplayEntity(t *testing.T, dir, feature, id, title, owner string) {
	t.Helper()
	data := map[string]any{"title": title}
	if feature == "task" {
		data["owner"] = owner
	}
	appendDisplayEvent(t, dir, feature, id, id, feature+".create", data)
}

func appendDisplayEvent(t *testing.T, dir, feature, id, eid, kind string, data map[string]any) {
	t.Helper()
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
	w := &entitychain.Writer{Dir: dir, RefFor: func(string) plumbing.ReferenceName {
		return plumbing.ReferenceName("refs/projects/" + feature + "/" + id)
	}, Registry: registry, TreeFiles: tree}
	_, err := w.Append(context.Background(), id, func() (*event.Envelope, string) {
		return &event.Envelope{V: event.SchemaVersion, Kind: kind, ID: eid, TS: "2026-07-04T00:00:00.000Z", Entity: id, Data: data}, "fixture"
	})
	if err != nil {
		t.Fatal(err)
	}
}

func listedDisplayIDs(t *testing.T, dir, feature string, args ...string) []string {
	t.Helper()
	result := mustRunCLI(t, dir, append([]string{feature, "list"}, args...)...)
	lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
	var ids []string
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("bad list row: %q", line)
		}
		ids = append(ids, fields[0])
	}
	return ids
}

func checkCopiedDisplayID(t *testing.T, dir, feature, display, full string) {
	t.Helper()
	result := mustRunCLI(t, dir, feature, "show", display, "--json")
	var output struct {
		Data struct {
			Meta struct {
				ID string `json:"id"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &output); err != nil {
		t.Fatal(err)
	}
	if output.Data.Meta.ID != full {
		t.Fatalf("copied display %s resolved to %s, want %s", display, output.Data.Meta.ID, full)
	}
}

func TestCLIListDisplaysReusableUniqueIDsAcrossFilters(t *testing.T) {
	for _, feature := range []string{"issue", "task", "chat"} {
		for _, fullRequired := range []bool{false, true} {
			name := feature + "/prefix"
			if fullRequired {
				name = feature + "/full"
			}
			t.Run(name, func(t *testing.T) {
				dir := newCLITestRepo(t)
				left := relatedID("01j8aaaa", "0")
				right := relatedID("01j8aaaa", "1")
				length := 9
				if fullRequired {
					right = left[:25] + "1"
					length = 26
				}
				seedDisplayEntity(t, dir, feature, left, "left", "left@example.com")
				seedDisplayEntity(t, dir, feature, right, "right", "right@example.com")
				displayed := listedDisplayIDs(t, dir, feature)
				if len(displayed) != 2 || displayed[0] == displayed[1] {
					t.Fatalf("list IDs collide: %v", displayed)
				}
				for i, full := range []string{left, right} {
					if len(displayed[i]) != length || !strings.HasPrefix(full, displayed[i]) {
						t.Fatalf("wrong shortest unique prefix: %v for %s", displayed, full)
					}
					checkCopiedDisplayID(t, dir, feature, displayed[i], full)
				}
				// The machine-readable identity stays full even when human IDs abbreviate.
				jsonList := mustRunCLI(t, dir, feature, "list", "--json")
				var output struct {
					Data struct {
						Items []map[string]any `json:"items"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(jsonList.stdout), &output); err != nil {
					t.Fatal(err)
				}
				if len(output.Data.Items) != 2 || output.Data.Items[0]["id"] != left || output.Data.Items[1]["id"] != right {
					t.Fatalf("JSON identity changed: %s", jsonList.stdout)
				}
				// Hide the competitor, including an archived issue/chat. Display must
				// still use the full same-feature namespace, not just visible rows.
				var filter []string
				switch feature {
				case "issue":
					svc := issueapp.New(dir)
					if ok, err := svc.Status(context.Background(), right, "closed"); err != nil || !ok {
						t.Fatalf("close fixture: %t %v", ok, err)
					}
					if ok, err := svc.Status(context.Background(), right, "archived"); err != nil || !ok {
						t.Fatalf("archive fixture: %t %v", ok, err)
					}
				case "task":
					filter = []string{"--owner", "left@example.com"}
				case "chat":
					if err := chatapp.New(dir).Archive(context.Background(), right); err != nil {
						t.Fatal(err)
					}
					filter = []string{"--status", "open"}
				}
				filtered := listedDisplayIDs(t, dir, feature, filter...)
				if len(filtered) != 1 || filtered[0] != displayed[0] {
					t.Fatalf("filtered display ignored hidden competitor: before=%v after=%v", displayed, filtered)
				}
				checkCopiedDisplayID(t, dir, feature, filtered[0], left)
			})
		}
	}
}

func TestCLIListKeepsEightCharacterIDsWhenUnique(t *testing.T) {
	for _, feature := range []string{"issue", "task", "chat"} {
		t.Run(feature, func(t *testing.T) {
			dir := newCLITestRepo(t)
			id := relatedID("01j8bbbb", "3")
			seedDisplayEntity(t, dir, feature, id, "only", "left@example.com")
			displayed := listedDisplayIDs(t, dir, feature)
			if len(displayed) != 1 || displayed[0] != id[:8] {
				t.Fatalf("want unique eight-character display, got %v", displayed)
			}
			checkCopiedDisplayID(t, dir, feature, displayed[0], id)
		})
	}
}

func shownEventIDs(t *testing.T, dir, feature, id string, args ...string) []string {
	t.Helper()
	result := mustRunCLI(t, dir, append([]string{feature, "show", id}, args...)...)
	var ids []string
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.HasPrefix(line, "--- ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				t.Fatalf("bad event header: %q", line)
			}
			ids = append(ids, fields[1])
		}
	}
	return ids
}

func TestCLIShowEventIDsAreReusableAndIncludeHiddenCandidates(t *testing.T) {
	f := newRelatedFixture(t)
	noteA, noteB := relatedID("01j9e000", "1"), relatedID("01j9e000", "2")
	appendDisplayEvent(t, f.dir, "task", f.task, noteA, "task.note", map[string]any{"body": "left note"})
	appendDisplayEvent(t, f.dir, "task", f.task, noteB, "task.note", map[string]any{"body": "right note"})
	for _, tc := range []struct {
		feature, id string
		full        []string
	}{{"issue", f.issue, []string{f.comment, f.comment2}}, {"task", f.task, []string{noteA, noteB}}, {"chat", f.chat, []string{f.message, f.message2}}} {
		shown := shownEventIDs(t, f.dir, tc.feature, tc.id)
		if len(shown) != 2 || shown[0] != tc.full[0][:9] || shown[1] != tc.full[1][:9] {
			t.Fatalf("event headers not uniquely abbreviated: %s %v", tc.feature, shown)
		}
	}
	commentIDs := shownEventIDs(t, f.dir, "issue", f.issue)
	mustRunCLI(t, f.dir, "issue", "comment", f.issue, "-m", "edited comment", "--reply-to", commentIDs[0], "--supersedes", commentIDs[1], "--no-sync", "--json")
	issue, err := issueapp.New(f.dir).Show(context.Background(), f.issue)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, comment := range issue.Comments {
		if comment["body"] == "edited comment" {
			found = true
			if comment["reply_to"] != f.comment || comment["supersedes"] != f.comment2 {
				t.Fatalf("copied comment IDs resolved incorrectly: %+v", comment)
			}
		}
	}
	if !found {
		t.Fatal("replacement comment missing")
	}
	allMessages := shownEventIDs(t, f.dir, "chat", f.chat)
	tailMessages := shownEventIDs(t, f.dir, "chat", f.chat, "--tail", "1")
	if len(tailMessages) != 1 || tailMessages[0] != allMessages[1] {
		t.Fatalf("tail ignored hidden message competitor: all=%v tail=%v", allMessages, tailMessages)
	}
	mustRunCLI(t, f.dir, "chat", "post", f.chat, "-m", "edited message", "--reply-to", tailMessages[0], "--supersedes", allMessages[0], "--no-sync", "--json")
	chat, err := chatapp.New(f.dir).Show(context.Background(), f.chat)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, message := range chat.Messages {
		if message["body"] == "edited message" {
			found = true
			if message["reply_to"] != f.message2 || message["supersedes"] != f.message {
				t.Fatalf("copied message IDs resolved incorrectly: %+v", message)
			}
		}
	}
	if !found {
		t.Fatal("replacement message missing")
	}
	// Human abbreviations do not leak into JSON event identities.
	for _, featureID := range []struct{ feature, id, collection string }{{"issue", f.issue, "comments"}, {"task", f.task, "notes"}, {"chat", f.chat, "messages"}} {
		result := mustRunCLI(t, f.dir, featureID.feature, "show", featureID.id, "--json")
		var payload struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
			t.Fatal(err)
		}
		for _, raw := range payload.Data[featureID.collection].([]any) {
			if len(raw.(map[string]any)["id"].(string)) != 26 {
				t.Fatalf("JSON event ID abbreviated: %s", result.stdout)
			}
		}
	}
}

func TestDisplayedInitialChatMessageCanBeRepliedToAndSuperseded(t *testing.T) {
	dir := newCLITestRepo(t)
	created := mustRunCLI(t, dir, "chat", "new", "--title", "thread", "-m", "first body", "--no-sync", "--json")
	var output struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(created.stdout), &output); err != nil {
		t.Fatal(err)
	}
	id := output.Data.ID
	initial, err := chatapp.New(dir).Show(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	firstID := initial.Messages[0]["id"].(string)
	shown := shownEventIDs(t, dir, "chat", id)
	if len(shown) != 1 || shown[0] != firstID[:8] {
		t.Fatalf("initial message ID missing: %v", shown)
	}
	mustRunCLI(t, dir, "chat", "post", id, "-m", "cli replacement", "--reply-to", shown[0], "--supersedes", shown[0], "--no-sync", "--json")
	result, err := newMcpTestSession(t, dir).CallTool(context.Background(), &mcp.CallToolParams{Name: "chat_post", Arguments: map[string]any{"id": id, "body": "mcp replacement", "reply_to": strings.ToUpper(firstID), "supersedes": strings.ToUpper(firstID)}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("MCP initial message reply: %s", result.Content[0].(*mcp.TextContent).Text)
	}
	chat, err := chatapp.New(dir).Show(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range chat.Messages {
		if message["body"] == "cli replacement" || message["body"] == "mcp replacement" {
			count++
			if message["reply_to"] != firstID || message["supersedes"] != firstID {
				t.Fatalf("initial message reference lost: %+v", message)
			}
		}
	}
	if count != 2 {
		t.Fatalf("missing replacement messages: %+v", chat.Messages)
	}
}
