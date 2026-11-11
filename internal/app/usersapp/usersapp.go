package usersapp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/idgen"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// RegistryRef is the users feature's single ref
// (docs/02-data-model.md「ref 名前空間」).
const RegistryRef = "refs/projects/users/registry"

// ErrIdentityNotConfigured means git's user.email is unset.
var ErrIdentityNotConfigured = identity.ErrNotConfigured

// ErrUserNotFound / ErrGroupNotFound mean no user/group with that name
// exists yet.
var (
	ErrUserNotFound  = errors.New("usersapp: user not found")
	ErrGroupNotFound = errors.New("usersapp: group not found")
)

// ErrInvalidName means a username/group name failed
// ValidUsername/ValidGroupname.
var ErrInvalidName = errors.New("usersapp: invalid name")

// ErrNameExists means a requested creation would reuse an occupied registry
// name. It is also an invalid-name usage error at the CLI boundary.
var ErrNameExists = fmt.Errorf("%w: name already exists", ErrInvalidName)

// ErrRetriesExhausted means a local CAS write kept losing to concurrent
// writers.
var ErrRetriesExhausted = entitychain.ErrRetriesExhausted

// User/Group are convenience aliases for fold's JSON-shaped maps.
type User = map[string]any
type Group = map[string]any

// Service operates on the users registry within a single repository
// directory.
type Service struct {
	Dir    string
	writer *entitychain.Writer
}

// New returns a Service rooted at dir (a githive-managed git repository).
func New(dir string) *Service {
	return &Service{
		Dir: dir,
		writer: &entitychain.Writer{
			Dir: dir,
			RefFor: func(string) plumbing.ReferenceName {
				return plumbing.ReferenceName(RegistryRef)
			},
			Registry:  materialize.UsersRegistry,
			TreeFiles: TreeFiles,
			// No NotFoundErr: the registry is a singleton with no
			// "create" event, like notify/stream.
		},
	}
}

func (s *Service) mutate(ctx context.Context, kind, summary string, data map[string]any) error {
	return s.mutateChecked(ctx, kind, summary, data, nil)
}

// Recheck predicates inside every CAS attempt so a competing local writer
// cannot claim the same namespace between a preflight read and the append.
func (s *Service) mutateChecked(ctx context.Context, kind, summary string, data map[string]any, check func(*materialize.State) error) error {
	_, err := s.writer.AppendOnto(ctx, "", func(string) (*event.Envelope, string, error) {
		if check != nil {
			state, err := s.writer.Fold(ctx, "")
			if err != nil {
				return nil, "", err
			}
			if err := check(state); err != nil {
				return nil, "", err
			}
		}
		eid, ts := idgen.NewWithTimestamp()
		return &event.Envelope{
			V: event.SchemaVersion, Kind: kind, ID: eid, TS: ts,
			Entity: eid, Data: data, Extra: map[string]any{},
		}, summary, nil
	})
	return err
}

// AddUser creates or updates a user record (docs/features/users.md
// 「イベント定義」users.user_set: LWW). Pass an empty string/nil slice for
// fields that should be left unchanged on an existing user.
func (s *Service) AddUser(ctx context.Context, username, display, email, kind string, roles []string) error {
	if !ValidUsername(username) {
		return fmt.Errorf("%w: username %q", ErrInvalidName, username)
	}
	if email != "" && !ValidEmail(email) {
		return fmt.Errorf("%w: %q (must contain '@', no whitespace, at most 320 bytes)", ErrInvalidEmail, email)
	}
	fields := map[string]any{}
	if display != "" {
		fields["display"] = display
	}
	if email != "" {
		fields["emails"] = []any{email}
	}
	if kind != "" {
		fields["kind"] = kind
	}
	if len(roles) > 0 {
		fields["roles"] = toAnySlice(roles)
	}
	return s.mutateChecked(ctx, "users.user_set", "users.user_set: "+username, map[string]any{
		"username": username,
		"fields":   fields,
	}, func(state *materialize.State) error {
		if _, exists := state.Collections["groups"][username]; exists {
			return fmt.Errorf("%w: %q belongs to a group", ErrNameExists, username)
		}
		return emailAvailable(state, username, email)
	})
}

// KeyAdd registers an SSH public key for username. username must already
// exist (docs/features/users.md: key ops target an existing user). pub must
// pass ValidatePublicKey, and a key whose material (type + base64, ignoring
// the comment) is already registered for username - active or revoked - is
// rejected with ErrKeyExists: re-adding a revoked key under a new comment
// would otherwise silently resurrect it.
func (s *Service) KeyAdd(ctx context.Context, username, pub string) error {
	pub = strings.TrimSpace(pub)
	if err := ValidatePublicKey(pub); err != nil {
		return err
	}
	user, err := s.getUser(ctx, username)
	if err != nil {
		return err
	}
	material := KeyMaterial(pub)
	for _, k := range userKeys(user) {
		if stored, _ := k["pub"].(string); KeyMaterial(stored) == material {
			return fmt.Errorf("%w: %s already has this key (%q)", ErrKeyExists, username, stored)
		}
	}
	return s.mutate(ctx, "users.key_add", "users.key_add: "+username, map[string]any{
		"username": username,
		"pub":      pub,
	})
}

// KeyRevoke marks username's key as revoked (not deleted). The key is
// matched by material (type + base64), so the comment given here need not
// match the registered one. fold matches users.key_revoke against the
// stored pub string exactly, so one event is written per matching
// registered entry, carrying that entry's exact string. If no active key
// matches, ErrKeyNotFound is returned instead of a silent no-op.
func (s *Service) KeyRevoke(ctx context.Context, username, pub string) error {
	pub = strings.TrimSpace(pub)
	if err := ValidatePublicKey(pub); err != nil {
		return err
	}
	user, err := s.getUser(ctx, username)
	if err != nil {
		return err
	}
	material := KeyMaterial(pub)
	var targets []string
	for _, k := range userKeys(user) {
		stored, _ := k["pub"].(string)
		if KeyMaterial(stored) == material && k["revoked_at"] == nil {
			targets = append(targets, stored)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("%w: %s has no active key matching %q", ErrKeyNotFound, username, material)
	}
	for _, stored := range targets {
		if err := s.mutate(ctx, "users.key_revoke", "users.key_revoke: "+username, map[string]any{
			"username": username,
			"pub":      stored,
		}); err != nil {
			return err
		}
	}
	return nil
}

func userKeys(user User) []map[string]any {
	raw, _ := user["keys"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// GroupSet creates or replaces a group.
func (s *Service) GroupSet(ctx context.Context, name string, members []string, description string) error {
	if !ValidGroupname(name) {
		return fmt.Errorf("%w: group name %q", ErrInvalidName, name)
	}
	data := map[string]any{
		"name":    name,
		"members": toAnySlice(members),
	}
	if description != "" {
		data["description"] = description
	}
	return s.mutateChecked(ctx, "users.group_set", "users.group_set: "+name, data, func(state *materialize.State) error {
		if _, exists := state.Collections["users"][name]; exists {
			return fmt.Errorf("%w: %q belongs to a user", ErrNameExists, name)
		}
		return nil
	})
}

// GroupRemove deletes a group.
func (s *Service) GroupRemove(ctx context.Context, name string) error {
	if !ValidGroupname(name) {
		return fmt.Errorf("%w: group name %q", ErrInvalidName, name)
	}
	return s.mutateChecked(ctx, "users.group_remove", "users.group_remove: "+name, map[string]any{"name": name}, func(state *materialize.State) error {
		if _, exists := state.Collections["groups"][name]; !exists {
			return ErrGroupNotFound
		}
		return nil
	})
}

// PolicySet replaces policy.json wholesale
// (docs/features/users.md「policy は部分編集イベントにせず全置換とする」).
func (s *Service) PolicySet(ctx context.Context, rules []any, def string) error {
	if err := ValidatePolicy(rules, def); err != nil {
		return err
	}
	return s.mutate(ctx, "users.policy_set", "users.policy_set", map[string]any{
		"rules":   rules,
		"default": def,
	})
}

func (s *Service) getUser(ctx context.Context, username string) (User, error) {
	state, err := s.writer.Fold(ctx, "")
	if err != nil {
		return nil, err
	}
	user, ok := state.Collections["users"][username].(map[string]any)
	if !ok {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// GetUser returns one user's record.
func (s *Service) GetUser(ctx context.Context, username string) (User, error) {
	return s.getUser(ctx, username)
}

// List returns every user and group, and the current policy (nil if never
// set).
func (s *Service) List(ctx context.Context) (users []User, groups []Group, policy map[string]any, err error) {
	state, err := s.writer.Fold(ctx, "")
	if err != nil {
		return nil, nil, nil, err
	}
	for _, raw := range state.Collections["users"] {
		if u, ok := raw.(map[string]any); ok {
			users = append(users, u)
		}
	}
	for _, raw := range state.Collections["groups"] {
		if g, ok := raw.(map[string]any); ok {
			groups = append(groups, g)
		}
	}
	sortRegistryLists(users, groups)
	return users, groups, materialize.Policy(state), nil
}

// ResolveToEmail resolves a CLI-provided target that may be a username or
// an already-fully-qualified email to an email address
// (docs/features/task.md「ユーザーとの紐づけ」, ADR-0009: events always
// store email, CLI accepts usernames and resolves them at write time). A
// bare string containing "@" is treated as already being an email and
// passed through unresolved (so this works even before P3's registry has
// any entries, per ADR-0009's "day 0" identity guarantee).
func (s *Service) ResolveToEmail(ctx context.Context, usernameOrEmail string) (string, error) {
	if containsAt(usernameOrEmail) {
		return usernameOrEmail, nil
	}
	user, err := s.getUser(ctx, usernameOrEmail)
	if err != nil {
		return "", fmt.Errorf("usersapp: cannot resolve username %q to an email: %w", usernameOrEmail, err)
	}
	emails := asStringSlice(user["emails"])
	if len(emails) == 0 {
		return "", fmt.Errorf("usersapp: user %q has no registered email", usernameOrEmail)
	}
	return emails[0], nil
}

func containsAt(s string) bool {
	for _, r := range s {
		if r == '@' {
			return true
		}
	}
	return false
}

func asStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// sortRegistryLists gives CLI, MCP and shallow views the same name order.
func sortRegistryLists(users []User, groups []Group) {
	sort.Slice(users, func(i, j int) bool { return users[i]["username"].(string) < users[j]["username"].(string) })
	sort.Slice(groups, func(i, j int) bool { return groups[i]["name"].(string) < groups[j]["name"].(string) })
}
