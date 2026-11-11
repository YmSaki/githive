package chain

import (
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestAppendEventSummaryRoundTrip is issue #78's regression test: whatever
// the summary carries (blank lines, CRLF, leading newlines, control
// characters), the written event must read back and the summary must stay
// on one line.
func TestAppendEventSummaryRoundTrip(t *testing.T) {
	requireGit(t)
	repo, err := OpenRepository(initBareDir(t))
	if err != nil {
		t.Fatal(err)
	}
	sig := Signature{Name: "Tester", Email: "tester@example.com", When: time.Now()}
	entity := "01j8x0a2b3c4d5e6f7g8h9j0ka"

	summaries := []string{
		"issue.comment: LGTM\n\none nit",
		"issue.comment: LGTM\r\n\r\none nit",
		"issue.comment: \n\nleading blank line",
		"\n\nissue.comment: starts with a blank line",
		"issue.comment: tab\tbell\a nul\x00 del\x7f",
		"issue.comment: \n\n{\"looks\":\"like json\"}",
		"issue.comment: " + strings.Repeat("長い要約\n\n", 30),
	}
	for i, summary := range summaries {
		env := testEnvelope(idAtSeq(i+1), entity, "issue.comment", map[string]any{"body": summary})
		h, err := AppendEvent(repo, ZeroHash, env, summary, map[string][]byte{"meta.json": []byte("{}\n")}, sig)
		if err != nil {
			t.Fatalf("summary %q: AppendEvent: %v", summary, err)
		}
		commit, err := repo.CommitObject(h)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ExtractEnvelope(commit)
		if err != nil {
			t.Fatalf("summary %q: ExtractEnvelope: %v\nmessage:\n%s", summary, err, commit.Message)
		}
		if got == nil || got.ID != env.ID || got.Data["body"] != summary {
			t.Fatalf("summary %q: decoded %+v", summary, got)
		}
		first, _, _ := strings.Cut(commit.Message, "\n")
		if strings.ContainsFunc(first, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Errorf("summary %q: first line still has control characters: %q", summary, first)
		}
		if !strings.HasPrefix(commit.Message[len(first):], "\n\n{") {
			t.Errorf("summary %q: summary spans more than one line:\n%s", summary, commit.Message)
		}
		if n := len([]rune(first)); n > 72 {
			t.Errorf("summary %q: first line is %d runes, want <= 72", summary, n)
		}
	}
}

// TestExtractEnvelopeReadsLegacyMultilineSummary checks that commits written
// before summaries were normalized - whose summary already contains a blank
// line - decode again (issue #78「既存の破損コミットも読める」).
func TestExtractEnvelopeReadsLegacyMultilineSummary(t *testing.T) {
	env := testEnvelope(idAtSeq(1), "01j8x0a2b3c4d5e6f7g8h9j0ka", "issue.comment", map[string]any{"body": "LGTM\n\none nit"})
	body, err := env.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{
		"issue.comment: LGTM\n\none nit",
		"issue.comment: LGTM\n\n\n\none nit",
		"issue.comment: \n\n{not json",
	} {
		commit := &object.Commit{Message: summary + "\n\n" + string(body)}
		got, err := ExtractEnvelope(commit)
		if err != nil {
			t.Fatalf("summary %q: %v", summary, err)
		}
		if got == nil || got.ID != env.ID {
			t.Fatalf("summary %q: decoded %+v", summary, got)
		}
	}
}

func TestExtractEnvelopeMergeCommitHasNoEvent(t *testing.T) {
	got, err := ExtractEnvelope(&object.Commit{Message: "merge: event-union", ParentHashes: []plumbing.Hash{plumbing.NewHash(strings.Repeat("1", 40)), plumbing.NewHash(strings.Repeat("2", 40))}})
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v; want nil, nil", got, err)
	}
}

func TestExtractEnvelopeRejectsMissingEventOnNonMerge(t *testing.T) {
	for _, parents := range [][]plumbing.Hash{nil, {plumbing.NewHash(strings.Repeat("1", 40))}} {
		for _, message := range []string{"ordinary commit", "merge: event-union", "summary\n\n"} {
			if _, err := ExtractEnvelope(&object.Commit{Message: message, ParentHashes: parents}); err == nil {
				t.Fatalf("accepted missing envelope with %d parent(s): %q", len(parents), message)
			}
		}
	}
}
