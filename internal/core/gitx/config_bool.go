package gitx

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ConfigBool reads a boolean using Git's own parser, preserving the distinction
// between missing, empty, and valueless keys. Invalid values return an error.
func (r *Runner) ConfigBool(ctx context.Context, key string, fallback bool) (bool, error) {
	out, err := r.run(ctx, "config", "--type=bool", "--get", key)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return fallback, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}
