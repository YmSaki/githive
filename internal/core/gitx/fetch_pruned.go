package gitx

import "context"

// FetchPruned fetches the supplied refspecs and removes tracking refs whose
// source no longer exists on this remote. In particular, the shared githive
// tracking namespace must describe the selected remote before init adopts it.
func (r *Runner) FetchPruned(ctx context.Context, remote string, refspecs ...string) error {
	return r.fetch(ctx, remote, true, refspecs...)
}

// fetch keeps options separate from positional arguments so a hardened fetch
// can terminate option parsing before accepting the remote and refspecs.
func (r *Runner) fetch(ctx context.Context, remote string, prune bool, refspecs ...string) error {
	if err := r.ValidateRemote(ctx, remote); err != nil {
		return err
	}
	args := []string{"fetch"}
	if prune {
		args = append(args, "--prune")
	}
	args = append(args, "--end-of-options", remote)
	args = append(args, refspecs...)
	_, err := r.run(ctx, args...)
	return err
}
