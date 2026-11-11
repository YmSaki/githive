package main

import (
	"fmt"
	"testing"

	"github.com/ymsaki/githive/internal/app/wikiapp"
)

func TestWikiLocalConflictReportsRecoveryTips(t *testing.T) {
	conflict := &wikiapp.LocalAdvanceConflictError{Current: "other-tip", Head: "editor-tip", Worktree: "/retained/wiki"}
	code, info := classifyError(fmt.Errorf("save: %w", conflict))
	if code != 3 || info.Code != "wiki_local_conflict" || info.Retryable {
		t.Fatalf("wrong classification: %d %+v", code, info)
	}
	data := info.Data
	if data["current"] != conflict.Current || data["head"] != conflict.Head || data["worktree"] != conflict.Worktree {
		t.Fatalf("missing recovery data: %#v", info.Data)
	}
}
