package event

import "testing"

func TestEncodeCompactLineKeyOrderAndEscaping(t *testing.T) {
	v := map[string]any{
		"zebra": "z",
		"apple": "a",
		"nested": map[string]any{
			"b": 2,
			"a": 1,
		},
		"list":  []any{"x", "y"},
		"quote": "say \"hi\"\nnewline",
	}
	got, err := EncodeCompactLine(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"apple": "a", "list": ["x", "y"], "nested": {"a": 1, "b": 2}, "quote": "say \"hi\"\nnewline", "zebra": "z"}`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
	if want[len(want)-1] == '\n' {
		t.Fatal("test setup error: want should not end in newline")
	}
}

func TestEncodeCompactLineEmptyContainers(t *testing.T) {
	got, err := EncodeCompactLine(map[string]any{"a": map[string]any{}, "b": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a": {}, "b": []}`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestEncodeCompactLineNoTrailingNewline(t *testing.T) {
	got, err := EncodeCompactLine(map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || got[len(got)-1] == '\n' {
		t.Errorf("expected no trailing newline, got %q", got)
	}
}

// TestEncodeCompactLineMatchesEncodeValues checks that compact and pretty
// encoders agree on scalar values by comparing against every canonical
// golden vector's input with the pretty-printed whitespace stripped is not
// meaningful (compact form differs deliberately in punctuation), so instead
// this test just exercises the float/number edge cases shared with the
// pretty encoder to confirm the same numeric rules apply.
func TestEncodeCompactLineNumberRules(t *testing.T) {
	got, err := EncodeCompactLine(map[string]any{
		"int_zero":         0,
		"float_int_valued": 1.0,
		"big_int":          int64(100000000000000000),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"big_int": 100000000000000000, "float_int_valued": 1, "int_zero": 0}`
	if got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}
