package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIInitAfterCloneJoinsExistingProject is issue #101's headline
// scenario: A inits and writes an issue, B clones and inits. B must adopt A's
// meta/config (one root, A's project name) instead of writing a second
// meta.init root that sync would merge over the shared config - and B must
// be able to read A's data right after init.
func TestCLIInitAfterCloneJoinsExistingProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	a := filepath.Join(tmp, "a")
	if out, err := exec.Command("git", "clone", "--quiet", origin, a).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	setCLIIdentity(t, a, "a@example.com")
	mustRunCLI(t, a, "init", "--project", "shared", "--json")
	mustRunCLI(t, a, "issue", "new", "--title", "from a", "--json")

	b := filepath.Join(tmp, "beta-clone")
	if out, err := exec.Command("git", "clone", "--quiet", origin, b).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	setCLIIdentity(t, b, "b@example.com")
	res := mustRunCLI(t, b, "init", "--json")
	var initOut struct {
		Data struct {
			MetaConfigMade bool `json:"meta_config_made"`
			Remote         struct {
				Fetched       bool     `json:"fetched"`
				HasMetaConfig bool     `json:"has_meta_config"`
				AdoptedRefs   []string `json:"adopted_refs"`
			} `json:"remote"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &initOut); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if initOut.Data.MetaConfigMade || !initOut.Data.Remote.Fetched || !initOut.Data.Remote.HasMetaConfig {
		t.Errorf("B's init should have joined the existing project: %s", res.stdout)
	}
	if !containsRef(initOut.Data.Remote.AdoptedRefs, "refs/projects/meta/config") {
		t.Errorf("meta/config not among adopted refs: %v", initOut.Data.Remote.AdoptedRefs)
	}

	// Readable straight after init, without a sync.
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	res = mustRunCLI(t, b, "issue", "list", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 1 {
		t.Errorf("expected A's issue right after init, got %+v", listed.Data.Items)
	}

	mustRunCLI(t, b, "sync", "--json")
	mustRunCLI(t, a, "sync", "--json")

	for _, dir := range []string{a, b} {
		out, err := exec.Command("git", "-C", dir, "show", "refs/projects/meta/config:config.json").CombinedOutput()
		if err != nil {
			t.Fatalf("git show config.json: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), `"project": "shared"`) {
			t.Errorf("%s: config.json project changed:\n%s", filepath.Base(dir), out)
		}
		roots, err := exec.Command("git", "-C", dir, "rev-list", "--max-parents=0", "refs/projects/meta/config").CombinedOutput()
		if err != nil {
			t.Fatalf("git rev-list: %v\n%s", err, roots)
		}
		if n := len(strings.Fields(string(roots))); n != 1 {
			t.Errorf("%s: meta/config has %d roots, want 1", filepath.Base(dir), n)
		}
	}
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

func TestCLIInitPublishRespectsNoSyncAndPreservesRejectedConfig(t *testing.T) {
	for _, noSync := range []bool{true, false} {
		t.Run(map[bool]string{true: "no-sync", false: "rejected"}[noSync], func(t *testing.T) {
			origin := filepath.Join(t.TempDir(), "origin.git")
			if out, err := exec.Command("git", "init", "--quiet", "--bare", origin).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			dir := newCLITestRepo(t)
			if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", origin).CombinedOutput(); err != nil {
				t.Fatalf("git remote add: %v %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			args := []string{"init", "--project", "local-project", "--json"}
			if noSync {
				args = append(args, "--no-sync")
			}
			res := mustRunCLI(t, dir, args...)
			var output struct {
				OK       bool `json:"ok"`
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(res.stdout), &output); err != nil {
				t.Fatal(err)
			}
			if !output.OK {
				t.Fatalf("local init failed: %s", res.stdout)
			}
			if noSync && len(output.Warnings) != 0 {
				t.Fatalf("no-sync attempted publication: %s", res.stdout)
			}
			if !noSync && (len(output.Warnings) != 1 || output.Warnings[0].Code != "sync_failed") {
				t.Fatalf("missing publication warning: %s", res.stdout)
			}
			if out, err := exec.Command("git", "-C", dir, "show", "refs/projects/meta/config:config.json").CombinedOutput(); err != nil || !strings.Contains(string(out), "local-project") {
				t.Fatalf("local config lost: %v %s", err, out)
			}
			if err := exec.Command("git", "-C", origin, "show-ref", "--verify", "refs/projects/meta/config").Run(); err == nil {
				t.Fatal("config unexpectedly published")
			}
		})
	}
}
