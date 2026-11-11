package event

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEventIdentityVectors(t *testing.T) {
	paths, err := filepath.Glob("../../../spec/vectors/event-identity/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("vectors: %v %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			value, err := DecodeGeneric(raw)
			if err != nil {
				t.Fatal(err)
			}
			vector := value.(map[string]any)
			var envs []*Envelope
			for _, input := range vector["events"].([]any) {
				env, err := FromMap(input.(map[string]any))
				if err != nil {
					t.Fatal(err)
				}
				envs = append(envs, env)
			}
			for _, reverse := range []bool{false, true} {
				var ids IDSet
				var got error
				freshCount := 0
				for i := range envs {
					j := i
					if reverse {
						j = len(envs) - 1 - i
					}
					fresh, err := ids.Add(envs[j])
					if err != nil {
						got = err
						break
					}
					if fresh {
						freshCount++
					}
				}
				if conflict := vector["conflicting"].(bool); errors.Is(got, ErrConflictingEventID) != conflict {
					t.Fatalf("reverse=%t error=%v want conflicting=%t", reverse, got, conflict)
				}
				if !vector["conflicting"].(bool) && (got != nil || freshCount != 1) {
					t.Fatalf("copies not deduplicated: fresh=%d err=%v", freshCount, got)
				}
			}
		})
	}
}
