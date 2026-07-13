package wikifs

import "testing"

// codesFor returns the set of violation codes reported for a given file set.
func codesFor(files []File) map[string]int {
	m := map[string]int{}
	for _, v := range Check(files) {
		m[v.Code]++
	}
	return m
}

func TestCheckCleanTree(t *testing.T) {
	files := []File{
		{"Home.md", 100},
		{"design/sync.md", 200},
		{"ops/release.md", 50},
		{"_assets/logo.png", MaxAssetBytes}, // exactly at the limit is allowed
	}
	if vs := Check(files); len(vs) != 0 {
		t.Fatalf("clean tree should have no violations, got %+v", vs)
	}
}

func TestCheckReservedNames(t *testing.T) {
	cases := []string{"CON", "con.md", "PRN.txt", "aux", "NUL.md", "COM1", "com9.md", "LPT1", "lpt9.log"}
	for _, name := range cases {
		if c := codesFor([]File{{name, 10}}); c["reserved_name"] == 0 {
			t.Errorf("%q: expected reserved_name violation, got %v", name, c)
		}
	}
	// A reserved word as a non-basename component (extension, substring) is fine.
	for _, ok := range []string{"console.md", "com10.md", "my-con.md", "config.md"} {
		if c := codesFor([]File{{ok, 10}}); c["reserved_name"] != 0 {
			t.Errorf("%q: unexpected reserved_name violation", ok)
		}
	}
}

func TestCheckTrailingDotSpace(t *testing.T) {
	for _, name := range []string{"bad.", "trailing ", "dir./file.md", "a/b /c.md"} {
		if c := codesFor([]File{{name, 10}}); c["trailing_dot_space"] == 0 {
			t.Errorf("%q: expected trailing_dot_space violation, got %v", name, c)
		}
	}
	if c := codesFor([]File{{"fine.md", 10}}); c["trailing_dot_space"] != 0 {
		t.Error("fine.md should not trip trailing_dot_space")
	}
}

func TestCheckInvalidChars(t *testing.T) {
	for _, name := range []string{"a:b.md", "q?.md", `back\slash.md`, "pipe|.md", "star*.md", "lt<.md"} {
		if c := codesFor([]File{{name, 10}}); c["invalid_char"] == 0 {
			t.Errorf("%q: expected invalid_char violation, got %v", name, c)
		}
	}
	if c := codesFor([]File{{"ok-name_1.md", 10}}); c["invalid_char"] != 0 {
		t.Error("ok-name_1.md should be valid")
	}
}

func TestCheckCaseCollision(t *testing.T) {
	files := []File{{"Home.md", 10}, {"home.md", 10}}
	c := codesFor(files)
	if c["case_collision"] != 1 {
		t.Errorf("expected exactly 1 case_collision, got %v", c)
	}
	// Same path twice is not a collision (identical, not case-differing).
	if c := codesFor([]File{{"Home.md", 10}, {"Home.md", 10}}); c["case_collision"] != 0 {
		t.Errorf("identical paths should not collide, got %v", c)
	}
	// Collision across directories differing only in case.
	if c := codesFor([]File{{"Design/a.md", 10}, {"design/a.md", 10}}); c["case_collision"] == 0 {
		t.Error("expected directory case collision")
	}
}

func TestCheckOversizeAsset(t *testing.T) {
	if c := codesFor([]File{{"_assets/big.png", MaxAssetBytes + 1}}); c["oversize_asset"] == 0 {
		t.Error("expected oversize_asset for >1MiB under _assets/")
	}
	// Over-1MiB outside _assets/ is not an asset violation (markdown can be large).
	if c := codesFor([]File{{"design/huge.md", MaxAssetBytes * 5}}); c["oversize_asset"] != 0 {
		t.Error("large non-asset file should not trip oversize_asset")
	}
}

func TestViolationPathPopulated(t *testing.T) {
	vs := Check([]File{{"CON.md", 10}})
	if len(vs) == 0 || vs[0].Path != "CON.md" || vs[0].Message == "" {
		t.Fatalf("violation should carry path+message, got %+v", vs)
	}
}
