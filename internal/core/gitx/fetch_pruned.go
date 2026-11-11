package gitx

import "context"

// FetchPruned fetches the supplied refspecs and removes tracking refs whose
// source no longer exists on this remote. In particular, the shared githive
// tracking namespace must describe the selected remote before init adopts it.
func (r *Runner) FetchPruned(ctx context.Context, remote string, refspecs ...string) error {
	args := append([]string{"fetch", "--prune", remote}, refspecs...)
	_, err := r.run(ctx, args...)
	return err
}
