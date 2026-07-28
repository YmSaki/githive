package materialize

import (
	"errors"
	"fmt"
)

// ErrCheckpointStateUnsupported means a checkpoint's data.state cannot be
// turned back into a fold state - typically because it predates the scratch
// key (ADR-0016) or was written by a newer tool. Callers treat it as "no
// shortcut available" and fall back to walking the whole chain.
var ErrCheckpointStateUnsupported = errors.New("materialize: checkpoint state is not in a restorable form")

// StateToMap renders a fold state as the JSON-shaped snapshot a checkpoint
// event carries in data.state: {"meta": …, "collections": …, "scratch": …}
// (ADR-0013 as extended by ADR-0016).
//
// A nil Meta is written as JSON null rather than {}, so that features which
// never populate Meta (users) round-trip back to nil instead of gaining an
// empty object that reducers would read as "this entity exists".
func StateToMap(s *State) (map[string]any, error) {
	var meta any
	if s.Meta != nil {
		meta = map[string]any(s.Meta)
	}

	collections := make(map[string]any, len(s.Collections))
	for name, coll := range s.Collections {
		collections[name] = map[string]any(coll)
	}

	scratch := make(map[string]any, len(s.Scratch))
	for key, value := range s.Scratch {
		encoded, err := scratchToJSON(value)
		if err != nil {
			return nil, fmt.Errorf("materialize: scratch %q: %w", key, err)
		}
		scratch[key] = encoded
	}

	return map[string]any{"meta": meta, "collections": collections, "scratch": scratch}, nil
}

// scratchToJSON normalizes the container types reducers keep in Scratch into
// the shapes event.Encode accepts. Scalars pass through untouched: they come
// from event data and are already whatever DecodeGeneric produced, and
// event.Encode is the authority on which of those it can represent.
func scratchToJSON(v any) (any, error) {
	switch t := v.(type) {
	case map[string]bool:
		out := make(map[string]any, len(t))
		for k, b := range t {
			out[k] = b
		}
		return out, nil
	case map[string][]string:
		out := make(map[string]any, len(t))
		for k, list := range t {
			arr := make([]any, len(list))
			for i, s := range list {
				arr[i] = s
			}
			out[k] = arr
		}
		return out, nil
	case []map[string]any:
		out := make([]any, len(t))
		for i, m := range t {
			encoded, err := scratchToJSON(m)
			if err != nil {
				return nil, err
			}
			out[i] = encoded
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			encoded, err := scratchToJSON(val)
			if err != nil {
				return nil, err
			}
			out[k] = encoded
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			encoded, err := scratchToJSON(val)
			if err != nil {
				return nil, err
			}
			out[i] = encoded
		}
		return out, nil
	case nil, bool, string:
		return v, nil
	default:
		// Numbers and anything else event.Encode understands pass through;
		// unknown container types are the dangerous case and Encode rejects
		// them with its own "unsupported type" error. Reaching here with a
		// map or slice type means a reducer introduced a Scratch shape
		// without teaching this function about it.
		return v, nil
	}
}

// StateFromMap rebuilds a fold state from StateToMap's output, restoring
// this feature's typed Scratch values through its registered decoder. It
// returns ErrCheckpointStateUnsupported when the snapshot is not in a shape
// this tool can restore, which callers treat as "walk the whole chain
// instead" (ADR-0016).
func (r *Registry) StateFromMap(m map[string]any) (*State, error) {
	s := NewState()

	if raw, ok := m["meta"]; ok && raw != nil {
		meta, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: meta is %T, want an object or null", ErrCheckpointStateUnsupported, raw)
		}
		s.Meta = meta
	}

	if raw, ok := m["collections"]; ok && raw != nil {
		collections, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: collections is %T, want an object", ErrCheckpointStateUnsupported, raw)
		}
		for name, value := range collections {
			coll, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: collection %q is %T, want an object", ErrCheckpointStateUnsupported, name, value)
			}
			s.Collections[name] = coll
		}
	}

	raw, ok := m["scratch"]
	if !ok {
		// Pre-ADR-0016 checkpoint: the scratch half of the fold state was
		// never written, so restoring from it would drop labels, acks,
		// policy and friends.
		return nil, fmt.Errorf("%w: no scratch key (checkpoint predates ADR-0016)", ErrCheckpointStateUnsupported)
	}
	scratch, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: scratch is %T, want an object", ErrCheckpointStateUnsupported, raw)
	}
	decoded, err := r.decodeScratch(scratch)
	if err != nil {
		return nil, err
	}
	s.Scratch = decoded
	return s, nil
}

// ScratchDecoder rebuilds one feature's typed Scratch map from the generic
// JSON objects StateToMap produced. Registries without Scratch leave it nil.
type ScratchDecoder func(map[string]any) (map[string]any, error)

// SetScratchDecoder installs the decoder used when restoring a checkpoint
// state for this feature. Every Scratch key a reducer writes must be
// covered; unknown keys make restoration fail (and therefore fall back to a
// full walk) rather than produce a half-restored state.
func (r *Registry) SetScratchDecoder(fn ScratchDecoder) {
	r.scratchDecoder = fn
}

func (r *Registry) decodeScratch(m map[string]any) (map[string]any, error) {
	if r.scratchDecoder == nil {
		if len(m) > 0 {
			return nil, fmt.Errorf("%w: scratch has %d key(s) but this feature registered no decoder", ErrCheckpointStateUnsupported, len(m))
		}
		return map[string]any{}, nil
	}
	decoded, err := r.scratchDecoder(m)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// scratchConverter turns one decoded JSON value back into the Go type a
// feature's reducers expect to find in Scratch.
type scratchConverter func(any) (any, error)

// decodeScratchKeys applies conv to each key, rejecting keys it does not
// know about.
func decodeScratchKeys(m map[string]any, conv map[string]scratchConverter) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for key, value := range m {
		fn, ok := conv[key]
		if !ok {
			return nil, fmt.Errorf("%w: unknown scratch key %q", ErrCheckpointStateUnsupported, key)
		}
		converted, err := fn(value)
		if err != nil {
			return nil, fmt.Errorf("%w: scratch %q: %v", ErrCheckpointStateUnsupported, key, err)
		}
		out[key] = converted
	}
	return out, nil
}

// scratchStringSet restores a map[string]bool used as a set.
func scratchStringSet(v any) (any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("want an object, got %T", v)
	}
	out := make(map[string]bool, len(m))
	for k, raw := range m {
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("member %q is %T, want a bool", k, raw)
		}
		out[k] = b
	}
	return out, nil
}

// scratchMapSlice restores a []map[string]any (links, status_history).
func scratchMapSlice(v any) (any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("want an array, got %T", v)
	}
	out := make([]map[string]any, len(arr))
	for i, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("element %d is %T, want an object", i, raw)
		}
		out[i] = m
	}
	return out, nil
}

// scratchStringListMap restores a map[string][]string (notify acks).
func scratchStringListMap(v any) (any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("want an object, got %T", v)
	}
	out := make(map[string][]string, len(m))
	for k, raw := range m {
		arr, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("value of %q is %T, want an array", k, raw)
		}
		list := make([]string, len(arr))
		for i, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("value of %q element %d is %T, want a string", k, i, item)
			}
			list[i] = s
		}
		out[k] = list
	}
	return out, nil
}

// scratchObject restores a map[string]any kept verbatim (users policy).
func scratchObject(v any) (any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("want an object, got %T", v)
	}
	return m, nil
}

// The decoders below mirror the Scratch keys each feature's reducers write.
// They live together so that the checkpoint round-trip is reviewable in one
// place (ADR-0016); the keys themselves are defined by the accessor helpers
// at the top of issue.go / task.go / chat.go / notify.go / users.go.

func decodeIssueScratch(m map[string]any) (map[string]any, error) {
	return decodeScratchKeys(m, map[string]scratchConverter{
		"labels":    scratchStringSet,
		"assignees": scratchStringSet,
		"links":     scratchMapSlice,
	})
}

func decodeTaskScratch(m map[string]any) (map[string]any, error) {
	return decodeScratchKeys(m, map[string]scratchConverter{
		"links":          scratchMapSlice,
		"status_history": scratchMapSlice,
	})
}

func decodeChatScratch(m map[string]any) (map[string]any, error) {
	return decodeScratchKeys(m, map[string]scratchConverter{
		"participants": scratchStringSet,
	})
}

func decodeNotifyScratch(m map[string]any) (map[string]any, error) {
	return decodeScratchKeys(m, map[string]scratchConverter{
		"acks": scratchStringListMap,
	})
}

func decodeUsersScratch(m map[string]any) (map[string]any, error) {
	return decodeScratchKeys(m, map[string]scratchConverter{
		"policy": scratchObject,
	})
}
