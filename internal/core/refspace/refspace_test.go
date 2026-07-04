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
