package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func setCLIIdentity(t *testing.T, dir, email string) {
	t.Helper()
	for _, kv := range [][2]string{{"user.email", email}, {"user.name", email}} {
		cmd := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
}

// TestCLIAutoNotifyOnIssueAssign verifies the auto-notify hook wired into
// `githive issue assign` (docs/features/notify.md「自動通知」).
func TestCLIAutoNotifyOnIssueAssign(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "issue", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID

	res = runCLI(t, dir, "issue", "assign", id, "--add", "b@example.com", "--no-sync")
	if res.code != 0 {
		t.Fatalf("issue assign failed: code=%d stderr=%s", res.code, res.stderr)
	}

	res = runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected 1 auto-generated notification, got %+v", listed.Data.Items)
	}
	targets, _ := listed.Data.Items[0]["targets"].([]any)
	if len(targets) != 1 || targets[0] != "user:b@example.com" {
		t.Errorf("unexpected notify targets: %v", targets)
	}
}

// TestCLIAutoNotifySkipsSelf verifies task.status->done does not notify the
// creator when the creator is the one who made the transition.
func TestCLIAutoNotifySkipsSelf(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t)

	res := runCLI(t, dir, "task", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("task new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID

	for _, to := range []string{"doing", "review", "done"} {
		res = runCLI(t, dir, "task", "status", id, to, "--no-sync")
		if res.code != 0 {
			t.Fatalf("task status %s failed: code=%d stderr=%s", to, res.code, res.stderr)
		}
	}

	res = runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 0 {
		t.Errorf("expected no self-notify on done transition, got %+v", listed.Data.Items)
	}
}

// TestCLIAutoNotifyOnTaskStatusDoneNotifiesCreator verifies a different
// actor completing the task does notify the original creator.
func TestCLIAutoNotifyOnTaskStatusDoneNotifiesCreator(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // identity: cli@example.com

	res := runCLI(t, dir, "task", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("task new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID

	setCLIIdentity(t, dir, "other@example.com")
	for _, to := range []string{"doing", "review", "done"} {
		res = runCLI(t, dir, "task", "status", id, to, "--no-sync")
		if res.code != 0 {
			t.Fatalf("task status %s failed: code=%d stderr=%s", to, res.code, res.stderr)
		}
	}

	res = runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	if len(listed.Data.Items) != 1 {
		t.Fatalf("expected 1 auto-generated notification to the creator, got %+v", listed.Data.Items)
	}
	targets, _ := listed.Data.Items[0]["targets"].([]any)
	if len(targets) != 1 || targets[0] != "user:cli@example.com" {
		t.Errorf("unexpected notify targets: %v", targets)
	}
}

func notifyItemCount(t *testing.T, dir string) int {
	t.Helper()
	res := runCLI(t, dir, "notify", "list", "--all", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("notify list failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var listed struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &listed); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	return len(listed.Data.Items)
}

// TestCLIAutoNotifyIssueAssignSkipsSelfAndAlreadyAssigned is a regression
// test for a review finding: `issue assign` did not skip notifying the
// actor assigning themselves, nor re-notifying someone already assigned
// (docs/features/notify.md「自動通知」only intends new-assignee notice).
func TestCLIAutoNotifyIssueAssignSkipsSelfAndAlreadyAssigned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // identity: cli@example.com

	res := runCLI(t, dir, "issue", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("issue new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID

	// Assigning to self must not notify self.
	if res := runCLI(t, dir, "issue", "assign", id, "--add", "cli@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("issue assign (self) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 0 {
		t.Errorf("expected 0 notifications after self-assign, got %d", got)
	}

	// Assigning b generates exactly one notification.
	if res := runCLI(t, dir, "issue", "assign", id, "--add", "b@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("issue assign (b) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 1 {
		t.Fatalf("expected 1 notification after assigning b, got %d", got)
	}

	// Re-adding b (already assigned) must not notify again.
	if res := runCLI(t, dir, "issue", "assign", id, "--add", "b@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("issue re-assign (b) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 1 {
		t.Errorf("expected re-adding an existing assignee not to notify again, got %d notifications", got)
	}
}

// TestCLIAutoNotifyTaskReassignSkipsSelfAndNoOp is the task.reassign
// analogue of the issue.assign regression test above.
func TestCLIAutoNotifyTaskReassignSkipsSelfAndNoOp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := newCLITestRepo(t) // identity: cli@example.com

	res := runCLI(t, dir, "task", "new", "--title", "t1", "--no-sync", "--json")
	if res.code != 0 {
		t.Fatalf("task new failed: code=%d stderr=%s", res.code, res.stderr)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &created); err != nil {
		t.Fatalf("bad json: %v\n%s", err, res.stdout)
	}
	id := created.Data.ID

	// Reassigning to self (the current owner, defaulted to actor at
	// creation) must not notify.
	if res := runCLI(t, dir, "task", "reassign", id, "--owner", "cli@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("task reassign (self/no-op) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 0 {
		t.Errorf("expected 0 notifications after no-op reassign to self, got %d", got)
	}

	// Reassigning to b generates exactly one notification.
	if res := runCLI(t, dir, "task", "reassign", id, "--owner", "b@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("task reassign (b) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 1 {
		t.Fatalf("expected 1 notification after reassigning to b, got %d", got)
	}

	// Reassigning to b again (no-op, already owner) must not notify again.
	if res := runCLI(t, dir, "task", "reassign", id, "--owner", "b@example.com", "--no-sync"); res.code != 0 {
		t.Fatalf("task reassign (b again) failed: code=%d stderr=%s", res.code, res.stderr)
	}
	if got := notifyItemCount(t, dir); got != 1 {
		t.Errorf("expected re-reassigning to the same owner not to notify again, got %d notifications", got)
	}
}
