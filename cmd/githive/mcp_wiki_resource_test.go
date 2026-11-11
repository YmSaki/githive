package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ymsaki/githive/internal/app/wikiapp"
)

func TestMcpWikiResourceMatchesShowWithoutWriting(t *testing.T) {
	dir := newCLITestRepo(t)
	ctx := context.Background()
	svc := wikiapp.New(dir)
	wt, err := svc.Edit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"Home.md":                []byte("# Hello\nこんにちは\n"),
		"design/nested page.md":  []byte("nested\n"),
		"_assets/non-utf8.bin":   {0, 0xff, 0xfe, 1},
		"design/percent%page.md": []byte("literal percent\n"),
	}
	for path, content := range files {
		full := filepath.Join(wt, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Save(ctx, "wiki resource fixtures", "origin", true); err != nil {
		t.Fatal(err)
	}
	refs := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname) %(objectname)").CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := refs()
	session := newMcpTestSession(t, dir)
	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, template := range templates.ResourceTemplates {
		found = found || template.URITemplate == "githive://wiki/{+path}"
	}
	if !found {
		t.Fatal("wiki resource template missing")
	}
	for path, escaped := range map[string]string{
		"Home.md": "Home.md", "design/nested page.md": "design/nested%20page.md",
		"_assets/non-utf8.bin": "_assets/non-utf8.bin", "design/percent%page.md": "design/percent%25page.md",
	} {
		t.Run(path, func(t *testing.T) {
			uri := "githive://wiki/" + escaped
			res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Contents) != 1 || res.Contents[0].URI != uri || res.Contents[0].MIMEType != "application/json" {
				t.Fatalf("unexpected resource: %+v", res.Contents)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(res.Contents[0].Text), &got); err != nil {
				t.Fatal(err)
			}
			want := callMcpTool(t, session, "wiki_show", map[string]any{"path": path})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("resource differs from wiki_show: got=%+v want=%+v", got, want)
			}
		})
	}
	for _, uri := range []string{"githive://wiki/Missing.md", "githive://wiki/"} {
		_, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err == nil || !strings.Contains(err.Error(), "Resource not found") {
			t.Errorf("%s: expected resource not found, got %v", uri, err)
		}
	}
	if after := refs(); after != before {
		t.Fatalf("read-only resources changed refs:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestMcpWikiResourcePreservesRepositoryErrors(t *testing.T) {
	session := newMcpTestSession(t, t.TempDir())
	_, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "githive://wiki/Home.md"})
	if err == nil || strings.Contains(err.Error(), "Resource not found") {
		t.Fatalf("repository error must not become a missing page: %v", err)
	}
}
