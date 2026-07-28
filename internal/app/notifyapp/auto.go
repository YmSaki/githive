package notifyapp

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/ymsaki/githive/internal/core/gitx"
)

// Config keys for the automatic notifications this file owns
// (docs/10-cli-spec.md「設定（git config）」).
const (
	// AutoConfigKey gates the four write-triggered notifications
	// (assign / reassign / reply / done). Default true.
	AutoConfigKey = "githive.notify.auto"
	// MentionsConfigKey gates @username / @group notifications from chat
	// bodies (docs/features/chat.md「メンションと通知」). Default true.
	MentionsConfigKey = "githive.notify.mentions"
)

// The wording of every automatic notification lives here and nowhere else.
// It used to be duplicated between the CLI and the MCP server, which meant
// the same action produced different notifications depending on which client
// performed it - contradicting docs/15-clients.md「全クライアントは機能の意味
// 論を再実装しない」. Clients now call the helpers below.
const (
	assignedTitleFormat = "%s %s の担当になりました"
	repliedTitleFormat  = "%s %s にコメントが返信されました"
	doneTitleFormat     = "%s %s が done になりました"
	mentionTitleFormat  = "chat %s であなたにメンションがありました"
)

// AutoEnabled reports whether write-triggered notifications are on. An
// unreadable or unset key means on, since the notification is a convenience
// and its absence is the surprising outcome.
func AutoEnabled(ctx context.Context, dir string) bool {
	return configEnabled(ctx, dir, AutoConfigKey)
}

// MentionsEnabled reports whether @mention notifications are on.
func MentionsEnabled(ctx context.Context, dir string) bool {
	return configEnabled(ctx, dir, MentionsConfigKey)
}

func configEnabled(ctx context.Context, dir, key string) bool {
	v, err := gitx.New(dir).ConfigGet(ctx, key)
	if err != nil || v == "" {
		return true
	}
	return v != "false" && v != "0"
}

// NotifyAssigned announces that assignee now owns entity id of feature
// ("issue"/"task"). It is a no-op when assignee is empty or is the actor
// themselves - nobody wants a notification about their own action
// (docs/features/notify.md「自動通知」).
//
// Every helper here returns warning strings rather than errors: a failed
// auto-notification must never fail the write that triggered it
// (docs/01-architecture.md「エラー処理方針」).
func NotifyAssigned(ctx context.Context, dir, feature, id, actor, assignee string) []string {
	return notifyUser(ctx, dir, assignee, actor,
		fmt.Sprintf(assignedTitleFormat, feature, shortID(id)),
		map[string]any{"kind": feature, "id": id})
}

// NotifyReplied announces that a comment replied to author's comment on
// entity id of feature.
func NotifyReplied(ctx context.Context, dir, feature, id, actor, author string) []string {
	return notifyUser(ctx, dir, author, actor,
		fmt.Sprintf(repliedTitleFormat, feature, shortID(id)),
		map[string]any{"kind": feature, "id": id})
}

// NotifyDone announces that entity id of feature reached its done state, to
// the person who created it.
func NotifyDone(ctx context.Context, dir, feature, id, actor, createdBy string) []string {
	return notifyUser(ctx, dir, createdBy, actor,
		fmt.Sprintf(doneTitleFormat, feature, shortID(id)),
		map[string]any{"kind": feature, "id": id})
}

func notifyUser(ctx context.Context, dir, target, actor, title string, source map[string]any) []string {
	if target == "" || target == actor {
		return nil
	}
	if !AutoEnabled(ctx, dir) {
		return nil
	}
	return post(ctx, dir, "user:"+target, title, source)
}

func post(ctx context.Context, dir, target, title string, source map[string]any) []string {
	if _, err := New(dir).Post(ctx, []string{target}, title, "", source, ""); err != nil {
		return []string{fmt.Sprintf("auto-notify to %s failed: %v", target, err)}
	}
	return nil
}

// mentionRe matches an @name token whose name obeys the username/group name
// rule shared by usersapp and spec/schemas/users.schema.json
// (^[a-z0-9][a-z0-9-]{1,38}$). The leading boundary keeps it from firing
// inside an email address, which is the common false positive in a chat body.
var mentionRe = regexp.MustCompile(`(^|[^\w@.-])@([a-z0-9][a-z0-9-]{1,38})\b`)

// ExtractMentions returns the distinct @names in body, in order of first
// appearance. It does not know which of them are users and which are groups;
// that is the resolver's job.
func ExtractMentions(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(body, -1) {
		name := m[2]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// MentionResolver turns an @name into the notify targets it stands for:
// one "user:<email>" for a username, or one per member for a group. It
// returns nil for a name the registry does not know, which is how an @word
// that merely looks like a mention is ignored.
//
// It is injected rather than imported so that notifyapp does not depend on
// usersapp - keeping the dependency one-way leaves usersapp free to notify
// in the future without creating an import cycle.
type MentionResolver func(ctx context.Context, name string) ([]string, error)

// NotifyMentions posts one notification per distinct target mentioned in a
// chat message body (docs/features/chat.md「メンションと通知」).
//
// Targets are resolved at post time and the resolution is not revisited
// later: chat.md specifies a snapshot of the registry as it stood when the
// message was written, so a group's membership changing afterwards does not
// retroactively change who was notified.
func NotifyMentions(ctx context.Context, dir, chatID, body, actor string, resolve MentionResolver) []string {
	if !MentionsEnabled(ctx, dir) {
		return nil
	}
	names := ExtractMentions(body)
	if len(names) == 0 {
		return nil
	}

	var warnings []string
	targets := map[string]bool{}
	for _, name := range names {
		resolved, err := resolve(ctx, name)
		if err != nil {
			// An unknown @name is not an error: chat bodies contain plenty of
			// @words that are not mentions.
			continue
		}
		for _, t := range resolved {
			if t == "" || t == "user:"+actor {
				continue // never notify the author of their own mention
			}
			targets[t] = true
		}
	}
	if len(targets) == 0 {
		return warnings
	}

	sorted := make([]string, 0, len(targets))
	for t := range targets {
		sorted = append(sorted, t)
	}
	sort.Strings(sorted)

	title := fmt.Sprintf(mentionTitleFormat, shortID(chatID))
	source := map[string]any{"kind": "chat", "id": chatID}
	for _, t := range sorted {
		warnings = append(warnings, post(ctx, dir, t, title, source)...)
	}
	return warnings
}

// shortID renders the 8-character prefix githive uses in human-facing text
// (docs/02-data-model.md「ID：ULID」: CLI は先頭 8 文字の短縮形を受け付ける).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
