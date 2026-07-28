package refspace

import "testing"

const sampleID = "01j8x0a2b3c4d5e6f7g8h9j0ka"

func TestEntityRef(t *testing.T) {
	ref, err := EntityRef(FeatureIssue, sampleID)
	if err != nil {
		t.Fatal(err)
	}
	want := "refs/projects/issue/" + sampleID
	if ref != want {
		t.Errorf("got %q want %q", ref, want)
	}

	if _, err := EntityRef(FeatureIssue, "not-a-ulid"); err == nil {
		t.Error("expected error for invalid ULID")
	}
	if _, err := EntityRef(FeatureMeta, sampleID); err == nil {
		t.Error("expected error for singleton feature")
	}
}

func TestParseSingletons(t *testing.T) {
	cases := map[string]Feature{
		MetaConfigRef:    FeatureMeta,
		NotifyStreamRef:  FeatureNotify,
		UsersRegistryRef: FeatureUsers,
		WikiMainRef:      FeatureWiki,
	}
	for ref, feature := range cases {
		p, err := Parse(ref)
		if err != nil {
			t.Errorf("%s: %v", ref, err)
			continue
		}
		if p.Feature != feature || p.ID != "" {
			t.Errorf("%s: got %+v", ref, p)
		}
	}
}

func TestParseEntityRef(t *testing.T) {
	ref := "refs/projects/task/" + sampleID
	p, err := Parse(ref)
	if err != nil {
		t.Fatal(err)
	}
	if p.Feature != FeatureTask || p.ID != sampleID {
		t.Errorf("got %+v", p)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	badRefs := []string{
		"refs/heads/main",
		"refs/projects/issue/not-a-ulid",
		"refs/projects/bogus/" + sampleID,
		"refs/projects/issue",
		"refs/projects/wiki/main/extra",
	}
	for _, ref := range badRefs {
		if _, err := Parse(ref); err == nil {
			t.Errorf("expected Parse(%q) to fail", ref)
		}
	}
}

func TestFilterByFeature(t *testing.T) {
	id2 := "01j8x0a2b3c4d5e6f7g8h9j0kb"
	refs := []string{
		"refs/projects/issue/" + sampleID,
		"refs/projects/task/" + id2,
		MetaConfigRef,
		"refs/heads/main",
	}
	got := FilterByFeature(refs, FeatureIssue)
	if len(got) != 1 || got[0] != "refs/projects/issue/"+sampleID {
		t.Errorf("got %v", got)
	}
}

// TestParseNotifyShardAccepted is D8 item 1: refs/projects/notify/<yyyy-mm>
// must parse as feature notify with the shard name captured separately from
// ID, not as a rejected "unknown feature" ref like before.
func TestParseNotifyShardAccepted(t *testing.T) {
	cases := []string{"2026-07", "2026-01", "2026-12", "1999-09"}
	for _, shard := range cases {
		ref := "refs/projects/notify/" + shard
		p, err := Parse(ref)
		if err != nil {
			t.Errorf("Parse(%q): %v", ref, err)
			continue
		}
		if p.Feature != FeatureNotify || p.Shard != shard || p.ID != "" {
			t.Errorf("Parse(%q) = %+v, want Feature=notify Shard=%q ID=\"\"", ref, p, shard)
		}
	}

	// The stream singleton must still parse exactly as before (Shard empty).
	p, err := Parse(NotifyStreamRef)
	if err != nil {
		t.Fatal(err)
	}
	if p.Feature != FeatureNotify || p.Shard != "" || p.ID != "" {
		t.Errorf("Parse(NotifyStreamRef) = %+v, want Shard=\"\" ID=\"\"", p)
	}
}

// TestParseNotifyShardRejectsMalformed pins the strict YYYY-MM check: only
// a 4-digit year and a 01-12 month are accepted, and any other name under
// notify/ (not "stream", not a well-formed shard) is still an error exactly
// like an unrecognized feature would be.
func TestParseNotifyShardRejectsMalformed(t *testing.T) {
	badRefs := []string{
		"refs/projects/notify/2026-13",  // month out of range
		"refs/projects/notify/2026-00",  // month out of range
		"refs/projects/notify/2026-1",   // month not zero-padded
		"refs/projects/notify/26-07",    // year not 4 digits
		"refs/projects/notify/2026-07x", // trailing garbage
		"refs/projects/notify/other",    // arbitrary non-shard name
		"refs/projects/notify/",         // empty name (also malformed: 2 empty-ish parts)
	}
	for _, ref := range badRefs {
		if _, err := Parse(ref); err == nil {
			t.Errorf("expected Parse(%q) to fail", ref)
		}
	}
}

// TestFilterByFeatureExcludesNotifyShards guards the constraint the D8 spec
// calls out explicitly: FilterByFeature's `parsed.ID != ""` gate must keep
// excluding notify shard refs (they have no entity ID, only a Shard name),
// so per-entity readers (issueapp/taskapp/chatapp) that filter their own
// feature are unaffected by notify's shard refs existing at all.
func TestFilterByFeatureExcludesNotifyShards(t *testing.T) {
	refs := []string{
		"refs/projects/issue/" + sampleID,
		"refs/projects/notify/2026-07",
		NotifyStreamRef,
	}
	if got := FilterByFeature(refs, FeatureNotify); len(got) != 0 {
		t.Errorf("expected no per-entity refs for FeatureNotify, got %v", got)
	}
	if got := FilterByFeature(refs, FeatureIssue); len(got) != 1 || got[0] != "refs/projects/issue/"+sampleID {
		t.Errorf("got %v", got)
	}
}

func TestRemoteTrackingRef(t *testing.T) {
	got, err := RemoteTrackingRef("refs/projects/issue/" + sampleID)
	if err != nil {
		t.Fatal(err)
	}
	want := "refs/githive-remote/issue/" + sampleID
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocalRefFromTrackingIsInverse(t *testing.T) {
	original := "refs/projects/issue/" + sampleID
	tracking, err := RemoteTrackingRef(original)
	if err != nil {
		t.Fatal(err)
	}
	back, err := LocalRefFromTracking(tracking)
	if err != nil {
		t.Fatal(err)
	}
	if back != original {
		t.Errorf("got %q want %q", back, original)
	}

	if _, err := LocalRefFromTracking("refs/heads/main"); err == nil {
		t.Error("expected error for a ref outside refs/githive-remote/")
	}
}
