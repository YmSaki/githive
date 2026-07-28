package issueapp

import (
	"os"
	"testing"
)

// TestMain neutralises the operator's global/system git config for every
// test in this package. These tests assert on githive's own behaviour, and
// an inherited commit.gpgsign + gpg.ssh.program would route every commit
// they create through the developer's signing setup - slower, and dependent
// on a key the tests know nothing about. Tests that are specifically about
// signing set the config they need on their own test repository, which this
// does not affect.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}
