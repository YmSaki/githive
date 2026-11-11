package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ymsaki/githive/internal/app/issueapp"
	"github.com/ymsaki/githive/internal/app/taskapp"
)

func TestCLIFileBodiesPreserveTrailingNewlines(t *testing.T) {
	for _, body := range []string{"x\n", "x\n\n", "x\r\n\r\n"} {
		t.Run(body, func(t *testing.T) {
			dir := newCLITestRepo(t)
			file := filepath.Join(t.TempDir(), "body.md")
			if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			newID := func(args ...string) string {
				args = append(args, "--no-sync", "--json")
				result := mustRunCLI(t, dir, args...)
				var response struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(result.stdout), &response); err != nil || response.Data.ID == "" {
					t.Fatalf("creation response: %v %s", err, result.stdout)
				}
				return response.Data.ID
			}
			issue := newID("issue", "new", "--title", "File body", "-F", file)
			mustRunCLI(t, dir, "issue", "comment", issue, "-F", file, "--no-sync", "--json")
			shown, err := issueapp.New(dir).Show(context.Background(), issue)
			if err != nil {
				t.Fatal(err)
			}
			if shown.Body != body || len(shown.Comments) != 1 || shown.Comments[0]["body"] != body {
				t.Fatalf("file body changed: body=%q comments=%v", shown.Body, shown.Comments)
			}
			task := newID("task", "new", "--title", "File note")
			mustRunCLI(t, dir, "task", "note", task, "-F", file, "--no-sync", "--json")
			taskShown, err := taskapp.New(dir).Show(context.Background(), task)
			if err != nil {
				t.Fatal(err)
			}
			if len(taskShown.Notes) != 1 || taskShown.Notes[0]["body"] != body {
				t.Fatalf("file note changed: %v", taskShown.Notes)
			}
		})
	}
}
