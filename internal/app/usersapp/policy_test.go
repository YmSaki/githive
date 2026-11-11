package usersapp

import (
	"context"
	"errors"
	"testing"
)

func TestPolicySetRejectsInvalidShapeWithoutChangingRegistry(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.PolicySet(ctx, []any{}, "deny"); err != nil {
		t.Fatal(err)
	}
	head := registryHead(t, svc)
	for _, tc := range []struct {
		name  string
		rules []any
		def   string
	}{
		{"missing rules", nil, "allow"},
		{"missing default", []any{}, ""},
		{"invalid default", []any{}, "denny"},
		{"null rule", []any{nil}, "deny"},
		{"nonobject rule", []any{"rule"}, "deny"},
		{"missing fields", []any{map[string]any{}}, "deny"},
		{"refs type", []any{map[string]any{"refs": 1, "allow": []any{}, "actions": []any{}}}, "deny"},
		{"allow type", []any{map[string]any{"refs": "refs/**", "allow": "role:admin", "actions": []any{}}}, "deny"},
		{"allow item", []any{map[string]any{"refs": "refs/**", "allow": []any{1}, "actions": []any{}}}, "deny"},
		{"null actions", []any{map[string]any{"refs": "refs/**", "allow": []any{}, "actions": nil}}, "deny"},
		{"invalid action", []any{map[string]any{"refs": "refs/**", "allow": []any{}, "actions": []any{"pussh"}}}, "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.PolicySet(ctx, tc.rules, tc.def); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("want ErrInvalidPolicy, got %v", err)
			}
			if registryHead(t, svc) != head {
				t.Fatal("invalid policy changed the registry")
			}
		})
	}
}

func TestPolicySetAcceptsDocumentedRulesAndSchemaExtensions(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	rules := []any{
		map[string]any{"refs": "refs/projects/users/*", "allow": []any{"role:admin"}, "actions": []any{"push"}},
		map[string]any{"refs": "refs/projects/wiki/*", "allow": []string{"group:core"}, "actions": []string{"push", "create", "delete", "force"}, "extension": true},
		map[string]any{"refs": "refs/projects/**", "allow": []any{"role:member"}, "actions": []any{"push"}},
	}
	if err := svc.PolicySet(context.Background(), rules, "deny"); err != nil {
		t.Fatal(err)
	}
	_, _, policy, err := svc.List(context.Background())
	if err != nil || policy["default"] != "deny" || len(policy["rules"].([]any)) != 3 {
		t.Fatalf("documented policy lost: %v, %v", policy, err)
	}
	if policy["rules"].([]any)[1].(map[string]any)["extension"] != true {
		t.Fatal("schema-permitted extension was lost")
	}
}
