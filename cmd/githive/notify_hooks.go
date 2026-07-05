package main

import (
	"context"
	"fmt"

	"github.com/ymsaki/githive/internal/app/notifyapp"
	"github.com/ymsaki/githive/internal/core/gitx"
)

// autoNotifyEnabled reads githive.notify.auto (default true,
// docs/10-cli-spec.md「設定（git config）」).
func autoNotifyEnabled(ctx context.Context, dir string) bool {
	v, err := gitx.New(dir).ConfigGet(ctx, "githive.notify.auto")
	if err != nil || v == "" {
		return true
	}
	return v != "false" && v != "0"
}

// autoNotify posts one notification if githive.notify.auto is enabled,
// swallowing (as a warning, not an error) any failure - auto-notify is a
// convenience, not a correctness requirement
// (docs/features/notify.md「自動通知」: コア層には置かず、CLI の書き込み
// コマンドが設定に応じて併発する).
func autoNotify(ctx context.Context, dir, target, title string, source map[string]any) []string {
	if !autoNotifyEnabled(ctx, dir) {
		return nil
	}
	if _, err := notifyapp.New(dir).Post(ctx, []string{target}, title, "", source, ""); err != nil {
		return []string{fmt.Sprintf("auto-notify to %s failed: %v", target, err)}
	}
	return nil
}
