package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
)

func configGuardGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Keep valid envelopes and parent history while corrupting only config.json.
func replaceConfigProjection(t *testing.T, dir, payload string, missing bool) {
	t.Helper()
	ctx := context.Background()
	old, err := gitx.New(dir).RevParse(ctx, "refs/projects/meta/config")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := chain.WalkChain(repo, plumbing.NewHash(old))
	if err != nil || len(events) != 1 {
		t.Fatalf("meta seed: %v %v", events, err)
	}
	files := map[string][]byte{}
	if !missing {
		files["config.json"] = []byte(payload)
	}
	sig, err := identity.Resolve(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := chain.AppendEvent(repo, plumbing.NewHash(old), events[0], "fixture config projection", files, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.AdvanceRef(dir, "refs/projects/meta/config", h, old); err != nil {
		t.Fatal(err)
	}
}

func TestCLIAndMcpRejectExistingInvalidRepositoryConfig(t *testing.T) {
	cases := []struct {
		name, raw, code string
		missing         bool
	}{
		{"missing file", "", "invalid_config", true}, {"malformed", "{broken", "invalid_config", false}, {"nonobject", "[]", "invalid_config", false},
		{"missing version", `{}`, "invalid_config", false}, {"fraction", `{"schema_version":1.9}`, "invalid_config", false}, {"rounded fraction", `{"schema_version":1.00000000000000000001}`, "invalid_config", false},
		{"zero", `{"schema_version":0}`, "invalid_config", false}, {"negative", `{"schema_version":-1}`, "invalid_config", false}, {"string", `{"schema_version":"999"}`, "invalid_config", false}, {"future", `{"schema_version":2}`, "schema_unsupported", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newCLITestRepo(t)
			mustRunCLI(t, dir, "init", "--project", "audit", "--no-sync", "--json")
			created := mustRunCLI(t, dir, "issue", "new", "--title", "existing", "--no-sync", "--json")
			var seed struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(created.stdout), &seed); err != nil {
				t.Fatal(err)
			}
			replaceConfigProjection(t, dir, tc.raw, tc.missing)
			before := configGuardGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/")
			for _, args := range [][]string{{"issue", "new", "--title", "blocked", "--no-sync", "--json"}, {"issue", "show", seed.Data.ID, "--json"}, {"init", "--no-sync", "--json"}} {
				res := runCLI(t, dir, args...)
				if res.code != 5 || !strings.Contains(res.stdout, `"code": "`+tc.code+`"`) {
					t.Fatalf("config should fail closed: args=%v result=%+v", args, res)
				}
				if after := configGuardGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/"); after != before {
					t.Fatal("failed config guard advanced a project ref")
				}
			}
			callMcpToolExpectError(t, newMcpTestSession(t, dir), "issue_new", map[string]any{"title": "blocked"})
			if after := configGuardGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/projects/"); after != before {
				t.Fatal("MCP config failure appended an event")
			}
			fsck := runCLI(t, dir, "fsck", "--json")
			finding := "invalid_config"
			if tc.missing {
				finding = "missing_config"
			}
			if tc.code == "schema_unsupported" {
				finding = "unsupported_schema_version"
			}
			if fsck.code != 4 || !strings.Contains(fsck.stdout, `"code": "`+finding+`"`) {
				t.Fatalf("fsck/parser disagreement: %+v", fsck)
			}
		})
	}
}

func TestInvalidRemoteConfigPreventsInitAndSyncAdoption(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
		missing         bool
	}{{"missing", "", "invalid_config", true}, {"fraction", `{"schema_version":1.5}`, "invalid_config", false}, {"future", `{"schema_version":2}`, "schema_unsupported", false}} {
		t.Run(tc.name, func(t *testing.T) {
			a := newCLITestRepo(t)
			origin := t.TempDir()
			configGuardGit(t, origin, "init", "--bare", "--quiet")
			configGuardGit(t, a, "remote", "add", "origin", origin)
			mustRunCLI(t, a, "init", "--project", "remote", "--no-sync", "--json")
			created := mustRunCLI(t, a, "issue", "new", "--title", "remote issue", "--no-sync", "--json")
			var seed struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(created.stdout), &seed); err != nil {
				t.Fatal(err)
			}
			replaceConfigProjection(t, a, tc.raw, tc.missing)
			configGuardGit(t, a, "push", "origin", "refs/projects/meta/config:refs/projects/meta/config", "refs/projects/issue/"+seed.Data.ID+":refs/projects/issue/"+seed.Data.ID)
			for _, command := range []string{"init", "sync"} {
				b := newCLITestRepo(t)
				configGuardGit(t, b, "remote", "add", "origin", origin)
				res := runCLI(t, b, command, "--json")
				if res.code != 5 || !strings.Contains(res.stdout, `"code": "`+tc.code+`"`) {
					t.Fatalf("remote config accepted by %s: %+v", command, res)
				}
				if refs := configGuardGit(t, b, "for-each-ref", "--format=%(refname)", "refs/projects/"); refs != "" {
					t.Fatalf("invalid config installed authoritative refs before rejection: %s", refs)
				}
			}
		})
	}
}

func TestAbsentConfigAndSupportedIntegralVersionRemainWritable(t *testing.T) {
	day0 := newCLITestRepo(t)
	mustRunCLI(t, day0, "issue", "new", "--title", "day zero", "--no-sync", "--json")
	if oid, err := gitx.New(day0).RevParse(context.Background(), "refs/projects/meta/config"); err != nil || oid != "" {
		t.Fatalf("day-zero behavior changed: %q %v", oid, err)
	}
	for _, version := range []string{"1", "1.0", "1e0", "1000e-3"} {
		t.Run(version, func(t *testing.T) {
			dir := newCLITestRepo(t)
			mustRunCLI(t, dir, "init", "--project", "supported", "--no-sync", "--json")
			replaceConfigProjection(t, dir, `{"schema_version":`+version+`}`, false)
			mustRunCLI(t, dir, "issue", "new", "--title", "supported", "--no-sync", "--json")
		})
	}
}
