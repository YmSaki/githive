package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/app/chatapp"
	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/materialize"
)

func seedDisplayEntity(t *testing.T, dir, feature, id, title, owner string) {
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
	data := map[string]any{"title": title}
	if feature == "task" {
		data["owner"] = owner
	}
	_, err := w.Append(context.Background(), id, func() (*event.Envelope, string) {
		return &event.Envelope{V: event.SchemaVersion, Kind: feature + ".create", ID: id, TS: "2026-07-04T00:00:00.000Z", Entity: id, Data: data}, "fixture"
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
