package main

import (
	"testing"

	"github.com/ymsaki/githive/internal/app/wikiapp"
)

func TestClassifyWikiConflictRequiresResolution(t *testing.T) {
	code, info := classifyError(&wikiapp.ConflictError{Paths: []string{"Home.md"}})
	if code != 3 || info.Code != "wiki_conflict" || info.Retryable {
		t.Fatalf("wiki conflict needs manual resolution: exit=%d info=%+v", code, info)
	}
}
