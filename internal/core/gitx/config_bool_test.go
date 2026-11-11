package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigBoolUsesGitParserAndMissingDefault(t *testing.T) {
	for _, tc := range []struct {
		name, setting           string
		fallback, want, invalid bool
	}{
		{"missing-true", "", true, true, false},
		{"missing-false", "", false, false, false},
		{"implicit", "[feature]\n enabled\n", false, true, false},
		{"empty", "[feature]\n enabled =\n", true, false, false},
		{"uppercase", "[feature]\n enabled = OFF\n", true, false, false},
		{"nonzero", "[feature]\n enabled = -2\n", false, true, false},
		{"invalid", "[feature]\n enabled = maybe\n", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initRepo(t, dir)
			f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tc.setting); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := New(dir).ConfigBool(context.Background(), "feature.enabled", tc.fallback)
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("ConfigBool=%v, %v; want %v invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
