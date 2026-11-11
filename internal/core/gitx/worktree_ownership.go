package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

const worktreeOwnerFile = ".githive-owner"

func canonicalWorktreePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func (r *Runner) worktreeOwner(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSpace(string(out)))
}

func (r *Runner) ownsWorktree(ctx context.Context, dir string) (bool, error) {
	if !filepath.IsAbs(dir) || filepath.Base(dir) != "worktree" {
		return false, nil
	}
	parent := filepath.Dir(dir)
	tmp, err := canonicalWorktreePath(os.TempDir())
	if err != nil {
		return false, err
	}
	actualParent, err := filepath.EvalSymlinks(parent)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if actualParent != parent || filepath.Dir(parent) != tmp || !strings.HasPrefix(filepath.Base(parent), "githive-wiki-") {
		return false, nil
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, err
	}
	owner, err := os.ReadFile(filepath.Join(parent, worktreeOwnerFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want, err := r.worktreeOwner(ctx)
	return err == nil && string(owner) == want, err
}
