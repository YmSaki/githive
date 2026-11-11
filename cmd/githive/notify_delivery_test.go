package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIAutomaticNotificationsShareWriteSync(t *testing.T) {
	for _, kind := range []string{"issue", "task", "chat"} {
		for _, noSync := range []bool{false, true} {
			name := kind
			if noSync {
				name += "-no-sync"
			}
			t.Run(name, func(t *testing.T) {
				dir := newCLITestRepo(t)
				origin := filepath.Join(t.TempDir(), "origin.git")
				for _, args := range [][]string{{"init", "--quiet", "--bare", origin}, {"-C", dir, "remote", "add", "origin", origin}} {
					if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
						t.Fatalf("git: %v %s", err, out)
					}
				}
				res := mustRunCLI(t, dir, kind, "new", "--title", "delivery", "--json", "--no-sync")
				var created struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(res.stdout), &created); err != nil || created.Data.ID == "" {
					t.Fatalf("missing id: %v %s", err, res.stdout)
				}
				var args []string
				switch kind {
				case "issue":
					args = []string{"issue", "assign", created.Data.ID, "--add", "bob@example.com"}
				case "task":
					args = []string{"task", "reassign", created.Data.ID, "--owner", "bob@example.com"}
				case "chat":
					mustRunCLI(t, dir, "users", "add", "bob", "--email", "bob@example.com", "--no-sync", "--json")
					args = []string{"chat", "post", created.Data.ID, "--message", "hello @bob"}
				}
				args = append(args, "--json")
				if noSync {
					args = append(args, "--no-sync")
				}
				mustRunCLI(t, dir, args...)
				const stream = "refs/projects/notify/stream"
				local, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", stream).Output()
				if err != nil {
					t.Fatalf("no automatic notification created: %v", err)
				}
				remote, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", stream).Output()
				if noSync {
					if err == nil {
						t.Fatal("no-sync delivered notification")
					}
				} else if err != nil || string(remote) != string(local) {
					t.Fatalf("automatic notification not delivered: %v local=%s remote=%s", err, local, remote)
				}
			})
		}
	}
}
