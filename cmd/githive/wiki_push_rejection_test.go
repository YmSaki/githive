package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIWikiSavePermanentPushRejection(t *testing.T) {
	dir := newCLITestRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	for _, args := range [][]string{
		{"init", "--bare", origin},
		{"-C", dir, "remote", "add", "origin", origin},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte("#!/bin/sh\necho denied >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit := runCLI(t, dir, "wiki", "edit", "--json")
	if edit.code != 0 {
		t.Fatalf("wiki edit: %+v", edit)
	}
	var result struct {
		Data struct {
			Worktree string `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(edit.stdout), &result); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result.Data.Worktree, "Home.md"), []byte("saved edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	save := runCLI(t, dir, "wiki", "save", "-m", "home", "--json")
	if save.code != 1 {
		t.Fatalf("want exit 1, got %+v", save)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(save.stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, save.stdout)
	}
	if envelope.OK || envelope.Error.Code != "push_rejected" || envelope.Error.Retryable {
		t.Fatalf("unexpected rejection envelope: %s", save.stdout)
	}
}
