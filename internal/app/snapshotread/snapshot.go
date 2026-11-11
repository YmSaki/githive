// Package snapshotread decodes head trees only for explicitly labelled read
// views. It must never supply state to an app writer, resolver, or verifier.
package snapshotread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/event"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
)

// Read decodes a shallow ref's existing materialized files, never its missing
// events. Callers must already have received ShallowHistoryError from a normal
// read. Projection bodies retain their persisted trailing newline verbatim.
func Read(ctx context.Context, dir, ref, feature string) (*materialize.State, error) {
	oid, err := gitx.New(dir).RevParse(ctx, ref)
	if err != nil {
		return nil, err
	}
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return nil, err
	}
	files, err := chain.ReadTree(repo, plumbing.NewHash(oid))
	if err != nil {
		return nil, err
	}
	s := &materialize.State{Collections: map[string]map[string]any{}, Scratch: map[string]any{}}
	decode := func(path string) (map[string]any, error) {
		raw, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("snapshot: missing %s", path)
		}
		value, err := event.DecodeGeneric(raw)
		if err != nil {
			return nil, err
		}
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("snapshot: %s is not an object", path)
		}
		return m, nil
	}
	switch feature {
	case "issue", "task", "chat":
		s.Meta, err = decode("meta.json")
		if err != nil {
			return nil, err
		}
		if id, ok := s.Meta["id"].(string); !ok || id != ref[strings.LastIndex(ref, "/")+1:] {
			return nil, fmt.Errorf("snapshot: meta ID does not match ref")
		}
		if feature != "chat" {
			body, ok := files["body.md"]
			if !ok {
				return nil, fmt.Errorf("snapshot: missing body.md")
			}
			if !utf8.Valid(body) {
				return nil, fmt.Errorf("snapshot: body.md is not UTF-8")
			}
			s.Meta["body"] = string(body)
		}
		collection := map[string]string{"issue": "comments", "task": "notes", "chat": "messages"}[feature]
		s.Collections[collection] = map[string]any{}
		for path, raw := range files {
			if !strings.HasPrefix(path, collection+"/") {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(path, collection+"/"), ".md")
			if !event.IsValidULID(id) || path != collection+"/"+id+".md" {
				return nil, fmt.Errorf("snapshot: invalid projection path %q", path)
			}
			m, err := projection(raw, id)
			if err != nil {
				return nil, fmt.Errorf("snapshot: %s: %w", path, err)
			}
			s.Collections[collection][id] = m
		}
	case "users":
		s.Collections["users"] = map[string]any{}
		s.Collections["groups"] = map[string]any{}
		for path := range files {
			for _, collection := range []string{"users", "groups"} {
				if !strings.HasPrefix(path, collection+"/") {
					continue
				}
				name := strings.TrimSuffix(strings.TrimPrefix(path, collection+"/"), ".json")
				field, kind := "username", "users.user_set"
				if collection == "groups" {
					field, kind = "name", "users.group_set"
				}
				if path != collection+"/"+name+".json" {
					return nil, fmt.Errorf("snapshot: invalid registry path %q", path)
				}
				m, err := decode(path)
				if err != nil {
					return nil, err
				}
				if m[field] != name {
					return nil, fmt.Errorf("snapshot: registry key does not match %s", path)
				}
				if err := event.ValidateStoredData(&event.Envelope{Kind: kind, Data: map[string]any{field: name}}); err != nil {
					return nil, err
				}
				s.Collections[collection][name] = m
			}
		}
		if _, ok := files["policy.json"]; ok {
			p, err := decode("policy.json")
			if err != nil {
				return nil, err
			}
			s.Scratch["policy"] = p
		}
	case "notify":
		s.Meta, err = decode("meta.json")
		if err != nil {
			return nil, err
		}
		s.Collections["posts"] = map[string]any{}
		for path, raw := range files {
			if !strings.HasPrefix(path, "events/") || !strings.HasSuffix(path, ".jsonl") {
				continue
			}
			for _, line := range bytes.Split(raw, []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				v, err := event.DecodeGeneric(line)
				if err != nil {
					return nil, err
				}
				m, ok := v.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("snapshot: invalid notification")
				}
				id, ok := m["id"].(string)
				if !ok || !event.IsValidULID(id) {
					return nil, fmt.Errorf("snapshot: invalid notification ID")
				}
				if _, exists := s.Collections["posts"][id]; exists {
					return nil, fmt.Errorf("snapshot: duplicate notification ID %s", id)
				}
				s.Collections["posts"][id] = m
			}
		}
		acks, err := decode("acks.json")
		if err != nil {
			return nil, err
		}
		typed := map[string][]string{}
		for id, raw := range acks {
			if !event.IsValidULID(id) {
				return nil, fmt.Errorf("snapshot: invalid ack ID")
			}
			actors, ok := raw.([]any)
			if !ok {
				return nil, fmt.Errorf("snapshot: invalid ack actors")
			}
			for _, actor := range actors {
				email, ok := actor.(string)
				if !ok {
					return nil, fmt.Errorf("snapshot: invalid ack actor")
				}
				typed[id] = append(typed[id], email)
			}
		}
		s.Scratch["acks"] = typed
	default:
		return nil, fmt.Errorf("snapshot: unsupported feature %s", feature)
	}
	return s, nil
}

// ListSource labels existing list snapshots only when one of their feature's refs
// has incomplete history. Unrelated shallow source branches are ignored.
func ListSource(ctx context.Context, dir, feature string) (string, error) {
	repo, err := chain.OpenRepository(dir)
	if err != nil {
		return "", err
	}
	boundaries, err := repo.Storer.Shallow()
	if err != nil {
		return "", err
	}
	if len(boundaries) == 0 {
		return "", nil
	}
	entries, err := gitx.New(dir).ForEachRef(ctx, "refs/projects/"+feature+"/")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		id := entry.Ref[strings.LastIndex(entry.Ref, "/")+1:]
		if !event.IsValidULID(id) {
			continue
		}
		err = chain.RequireCompleteHistory(repo, plumbing.NewHash(entry.OID))
		var shallow *chain.ShallowHistoryError
		if errors.As(err, &shallow) {
			return "snapshot", nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

func projection(raw []byte, id string) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("projection is not UTF-8")
	}
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, fmt.Errorf("missing front matter")
	}
	end := bytes.Index(raw[4:], []byte("---\n\n"))
	if end < 0 {
		return nil, fmt.Errorf("unterminated front matter")
	}
	end += 4
	m := map[string]any{"body": string(raw[end+5:]), "body_source": "rendered_markdown"}
	for _, line := range strings.Split(string(raw[4:end]), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("invalid front matter")
		}
		if key == "event" {
			key = "id"
		}
		switch key {
		case "id", "reply_to", "supersedes", "superseded_by":
			if !event.IsValidULID(value) {
				return nil, fmt.Errorf("invalid %s", key)
			}
		case "author", "ts":
		default:
			return nil, fmt.Errorf("unknown front matter field %s", key)
		}
		if _, exists := m[key]; exists {
			return nil, fmt.Errorf("duplicate front matter field %s", key)
		}
		m[key] = value
	}
	if m["id"] != id {
		return nil, fmt.Errorf("projection ID mismatch")
	}
	return m, nil
}
