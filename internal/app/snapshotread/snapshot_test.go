package snapshotread

import "testing"

func TestProjectionRetainsRenderedBytesAndRejectsInvalidUTF8(t *testing.T) {
	id := "01j8xq4d3nbz9k7w2m5e8h1t61"
	prefix := "---\nauthor: a@example.com\nts: 2026-07-04T00:00:00.000Z\nevent: " + id + "\n---\n\n"
	for _, body := range []string{"x\n", "\n", "x\n\n"} {
		m, err := projection([]byte(prefix+body), id)
		if err != nil || m["body"] != body || m["body_source"] != "rendered_markdown" {
			t.Fatalf("rendering was normalized: %v %v", m, err)
		}
	}
	if _, err := projection(append([]byte(prefix), 0xff), id); err == nil {
		t.Fatal("invalid UTF-8 was silently replaced")
	}
}
