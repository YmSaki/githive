package usersapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAddUserRejectsOtherOwnerWithoutAppending(t *testing.T) {
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "", "shared@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	before := registryHead(t, svc)
	if err := svc.AddUser(ctx, "robot", "", "shared@example.com", "agent", nil); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate owner accepted: %v", err)
	}
	if registryHead(t, svc) != before {
		t.Fatal("rejection appended")
	}
	if err := svc.AddUser(ctx, "alice", "updated", "shared@example.com", "", nil); err != nil {
		t.Fatalf("same owner update denied: %v", err)
	}
	keyDir := filepath.Join(t.TempDir(), "not-created")
	before = registryHead(t, svc)
	if _, err := AddAgent(ctx, svc.Dir, "another-agent", "project", "shared@example.com", keyDir); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("agent duplicate accepted: %v", err)
	}
	if registryHead(t, svc) != before {
		t.Fatal("agent rejection appended")
	}
	if _, err := os.Stat(keyDir); !os.IsNotExist(err) {
		t.Fatalf("agent rejection created keydir: %v", err)
	}
}

func TestConcurrentLocalEmailClaimsHaveOneOwner(t *testing.T) {
	svc := newTestRepo(t)
	ctx := context.Background()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"alice", "robot"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			results <- New(svc.Dir).AddUser(ctx, name, "", "shared@example.com", "human", nil)
		}(name)
	}
	wg.Wait()
	close(results)
	good, denied := 0, 0
	for err := range results {
		if err == nil {
			good++
		} else if errors.Is(err, ErrEmailExists) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if good != 1 || denied != 1 {
		t.Fatalf("owners=%d denied=%d", good, denied)
	}
}
