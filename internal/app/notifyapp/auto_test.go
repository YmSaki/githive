package notifyapp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func newAutoTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "me@example.com"}, {"user.name", "Me"}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	return dir
}

func postTitles(t *testing.T, dir string) []string {
	t.Helper()
	posts, err := New(dir).List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, 0, len(posts))
	for _, p := range posts {
		title, _ := p["title"].(string)
		titles = append(titles, title)
	}
	return titles
}

// TestNotifyAssignedSuppressesSelf pins the two suppression rules the CLI and
// the MCP server both used to implement separately.
func TestNotifyAssignedSuppressesSelf(t *testing.T) {
	requireGit(t)
	dir := newAutoTestRepo(t)
	ctx := context.Background()

	if w := NotifyAssigned(ctx, dir, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", "me@example.com"); w != nil {
		t.Errorf("assigning yourself should not notify: %v", w)
	}
	if w := NotifyAssigned(ctx, dir, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", ""); w != nil {
		t.Errorf("an empty assignee should not notify: %v", w)
	}
	if got := postTitles(t, dir); len(got) != 0 {
		t.Fatalf("expected no notifications, got %v", got)
	}

	if w := NotifyAssigned(ctx, dir, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", "other@example.com"); w != nil {
		t.Fatalf("unexpected warnings: %v", w)
	}
	got := postTitles(t, dir)
	if len(got) != 1 || !strings.Contains(got[0], "の担当になりました") {
		t.Fatalf("unexpected notifications: %v", got)
	}
	if !strings.HasPrefix(got[0], "issue 01j8x0a2") {
		t.Errorf("expected the feature and the 8-char id in the title, got %q", got[0])
	}
}

// TestAutoDisabledSuppressesEverything covers githive.notify.auto=false.
func TestAutoDisabledSuppressesEverything(t *testing.T) {
	requireGit(t)
	dir := newAutoTestRepo(t)
	ctx := context.Background()
	if out, err := exec.Command("git", "-C", dir, "config", AutoConfigKey, "false").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	NotifyAssigned(ctx, dir, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", "other@example.com")
	NotifyReplied(ctx, dir, "issue", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", "other@example.com")
	NotifyDone(ctx, dir, "task", "01j8x0a2b3c4d5e6f7g8h9j0ka", "me@example.com", "other@example.com")

	if got := postTitles(t, dir); len(got) != 0 {
		t.Errorf("githive.notify.auto=false must suppress every automatic notification, got %v", got)
	}
}

func TestExtractMentions(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"plain", "hey @alice can you look?", []string{"alice"}},
		{"multiple and dedupe", "@alice @bob @alice", []string{"alice", "bob"}},
		{"start of body", "@alice first", []string{"alice"}},
		{"group", "ping @reviewers please", []string{"reviewers"}},
		{"email is not a mention", "mail me at bob@example.com", nil},
		{"uppercase is not a username", "@Alice", nil},
		{"too short", "@a", nil},
		{"punctuation after", "thanks @alice!", []string{"alice"}},
		{"code-ish", "see user@host and @alice", []string{"alice"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractMentions(c.body)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("ExtractMentions(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

// TestNotifyMentionsResolvesAndSuppressesSelf checks the mention path end to
// end with a stub resolver: a username, a group that expands to two members,
// an unknown name that is silently ignored, and the author's own address
// which must not be notified.
func TestNotifyMentionsResolvesAndSuppressesSelf(t *testing.T) {
	requireGit(t)
	dir := newAutoTestRepo(t)
	ctx := context.Background()

	resolve := func(_ context.Context, name string) ([]string, error) {
		switch name {
		case "alice":
			return []string{"user:alice@example.com"}, nil
		case "reviewers":
			return []string{"user:alice@example.com", "user:bob@example.com"}, nil
		case "me":
			return []string{"user:me@example.com"}, nil
		default:
			return nil, errUnknownName
		}
	}

	warnings := NotifyMentions(ctx, dir, "01j8x0a2b3c4d5e6f7g8h9j0ka",
		"@alice @reviewers @me @nobody-here", "me@example.com", resolve)
	if warnings != nil {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	posts, err := New(dir).List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{}
	for _, p := range posts {
		for _, to := range asAnySlice(p["targets"]) {
			if s, ok := to.(string); ok {
				targets[s] = true
			}
		}
		if title, _ := p["title"].(string); !strings.Contains(title, "メンション") {
			t.Errorf("unexpected notification title %q", title)
		}
	}
	if len(targets) != 2 || !targets["user:alice@example.com"] || !targets["user:bob@example.com"] {
		t.Errorf("expected alice and bob to be notified exactly once each, got %v", targets)
	}
	if targets["user:me@example.com"] {
		t.Error("the author must not be notified of their own mention")
	}
}

// TestMentionsDisabledSuppresses covers githive.notify.mentions=false.
func TestMentionsDisabledSuppresses(t *testing.T) {
	requireGit(t)
	dir := newAutoTestRepo(t)
	ctx := context.Background()
	if out, err := exec.Command("git", "-C", dir, "config", MentionsConfigKey, "false").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	resolve := func(_ context.Context, string2 string) ([]string, error) {
		return []string{"user:alice@example.com"}, nil
	}
	NotifyMentions(ctx, dir, "01j8x0a2b3c4d5e6f7g8h9j0ka", "@alice", "me@example.com", resolve)

	if got := postTitles(t, dir); len(got) != 0 {
		t.Errorf("githive.notify.mentions=false must suppress mention notifications, got %v", got)
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

const errUnknownName = stringError("unknown name")

func asAnySlice(v any) []any {
	arr, _ := v.([]any)
	return arr
}
