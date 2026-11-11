package projectconfig

import (
	"errors"
	"testing"
)

func TestVersionUsesExactPositiveJSONIntegerSemantics(t *testing.T) {
	for _, tc := range []struct {
		value                string
		version              int
		invalid, unsupported bool
	}{
		{"1", 1, false, false}, {"1.0", 1, false, false}, {"1e0", 1, false, false}, {"0.1e1", 1, false, false}, {"1000e-3", 1, false, false},
		{"2", 2, false, true}, {"1e3", 1000, false, true}, {"999999999999999999999999999999999999", 0, false, true}, {"1e99999999999999999999999", 0, false, true},
		{"0", 0, true, false}, {"-1", 0, true, false}, {"1.5", 0, true, false}, {"1.00000000000000000001", 0, true, false}, {"1e-99999999999999999999999", 0, true, false},
		{`"1"`, 0, true, false}, {`null`, 0, true, false}, {`true`, 0, true, false}, {`{}`, 0, true, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			raw := []byte(`{"schema_version":` + tc.value + `}`)
			got, err := Version("ref", raw)
			var invalid *InvalidError
			var future *UnsupportedError
			if tc.invalid {
				if !errors.As(err, &invalid) {
					t.Fatalf("want invalid config, got %d %v", got, err)
				}
				return
			}
			if err == nil && got != tc.version {
				t.Fatalf("got %d want %d", got, tc.version)
			}
			err = Check("ref", raw)
			if tc.unsupported {
				if !errors.As(err, &future) {
					t.Fatalf("want typed unsupported: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{`{}`, `[]`, `{broken`} {
		if err := Check("ref", []byte(raw)); err == nil {
			t.Fatalf("invalid config accepted: %s", raw)
		}
	}
}
