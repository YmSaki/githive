package usersapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ymsaki/githive/internal/core/gitx"
)

func registryHead(t *testing.T, svc *Service) string {
	t.Helper()
	head, err := gitx.New(svc.Dir).RevParse(context.Background(), RegistryRef)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func TestAddAgentPreservesExistingHuman(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.AddUser(ctx, "alice", "Alice", "alice@example.com", "human", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.GetUser(ctx, "alice")
	head := registryHead(t, svc)
	keyDir := filepath.Join(t.TempDir(), "not-created")
	if _, err := AddAgent(ctx, svc.Dir, "alice", "project", "", keyDir); !errors.Is(err, ErrNameExists) {
		t.Fatalf("want ErrNameExists, got %v", err)
	}
	after, err := svc.GetUser(ctx, "alice")
	if err != nil || !reflect.DeepEqual(before, after) || registryHead(t, svc) != head {
		t.Fatalf("human changed: before=%v after=%v err=%v", before, after, err)
	}
	if _, err := os.Stat(keyDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejection created key directory: %v", err)
	}
}

func TestAddAgentRejectsInvalidIdentityBeforeCreatingKeys(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	for _, project := range []string{"My Project", "MyProject", ".", "-project", "project-", "a.b", strings.Repeat("a", 64)} {
		keyDir := filepath.Join(t.TempDir(), "not-created")
		if _, err := AddAgent(context.Background(), svc.Dir, "agent01", project, "", keyDir); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("project %q: want ErrInvalidName, got %v", project, err)
		}
		if _, err := os.Stat(keyDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("project %q created key directory: %v", project, err)
		}
	}
	keyDir := filepath.Join(t.TempDir(), "not-created")
	if _, err := AddAgent(context.Background(), svc.Dir, "agent01", "project", "bad email", keyDir); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("want ErrInvalidEmail, got %v", err)
	}
	if _, err := os.Stat(keyDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid email created key directory: %v", err)
	}
	if head := registryHead(t, svc); head != "" {
		t.Fatalf("invalid identity changed registry: %s", head)
	}
}

func TestAddAgentPreservesKeyFilesAndSeparatesProjects(t *testing.T) {
	requireGit(t)
	requireSSHKeygen(t)
	ctx := context.Background()
	keyDir := t.TempDir()
	first := newTestRepo(t)
	setup, err := AddAgent(ctx, first.Dir, "agent01", "project-one", "", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(setup.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(setup.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(setup.PrivateKey) {
		t.Fatalf("key path is relative: %s", setup.PrivateKey)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(setup.PrivateKey)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private key permissions: %v %v", info, err)
		}
	}
	second := newTestRepo(t)
	if _, err := AddAgent(ctx, second.Dir, "agent01", "project-one", "", keyDir); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("want ErrKeyExists, got %v", err)
	}
	gotPrivate, _ := os.ReadFile(setup.PrivateKey)
	gotPublic, _ := os.ReadFile(setup.PublicKey)
	if string(gotPrivate) != string(private) || string(gotPublic) != string(public) || registryHead(t, second) != "" {
		t.Fatal("collision modified existing keys or registry")
	}
	other, err := AddAgent(ctx, second.Dir, "agent01", "project-two", "", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if other.PrivateKey == setup.PrivateKey {
		t.Fatal("different projects reused the same key path")
	}
	// A public key alone must also prevent setup, not get overwritten by keygen.
	if err := os.Remove(setup.PrivateKey); err != nil {
		t.Fatal(err)
	}
	third := newTestRepo(t)
	if _, err := AddAgent(ctx, third.Dir, "agent01", "project-one", "", keyDir); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("public-only collision: want ErrKeyExists, got %v", err)
	}
	gotPublic, _ = os.ReadFile(setup.PublicKey)
	if string(gotPublic) != string(public) || registryHead(t, third) != "" {
		t.Fatal("public-only collision modified existing key or registry")
	}
}

func TestRegistryNameCollisionBothDirectionsAndConcurrent(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	for _, groupFirst := range []bool{false, true} {
		svc := newTestRepo(t)
		user := func() error { return svc.AddUser(ctx, "shared", "", "", "human", nil) }
		group := func() error { return svc.GroupSet(ctx, "shared", nil, "") }
		first, second := user, group
		if groupFirst {
			first, second = group, user
		}
		if err := first(); err != nil {
			t.Fatal(err)
		}
		head := registryHead(t, svc)
		if err := second(); !errors.Is(err, ErrNameExists) || !errors.Is(err, ErrInvalidName) {
			t.Fatalf("want invalid-name collision, got %v", err)
		}
		if registryHead(t, svc) != head {
			t.Fatal("rejected collision appended an event")
		}
	}
	svc := newTestRepo(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- New(svc.Dir).AddUser(ctx, "shared", "", "", "human", nil) }()
	go func() { <-start; results <- New(svc.Dir).GroupSet(ctx, "shared", nil, "") }()
	close(start)
	first, second := <-results, <-results
	if !(first == nil && errors.Is(second, ErrNameExists) || second == nil && errors.Is(first, ErrNameExists)) {
		t.Fatalf("concurrent claims: %v / %v", first, second)
	}
}

func TestAddAgentCleansGeneratedKeysWhenRegistrationFails(t *testing.T) {
	requireGit(t)
	requireSSHKeygen(t)
	svc := newTestRepo(t)
	// An incomplete signing configuration makes the registry append fail,
	// after key generation but before any identity is committed.
	cmd := exec.Command("git", "-C", svc.Dir, "config", "commit.gpgsign", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("configure signing: %v: %s", err, out)
	}
	keyDir := t.TempDir()
	if _, err := AddAgent(context.Background(), svc.Dir, "agent01", "project", "", keyDir); err == nil {
		t.Fatal("expected signing configuration failure")
	}
	files, err := os.ReadDir(keyDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("failed registration left generated keys: %v, %v", files, err)
	}
	if head := registryHead(t, svc); head != "" {
		t.Fatalf("failed registration committed an identity: %s", head)
	}
}

func TestAgentKeyCreationRefusesFilesCreatedAfterPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("existing private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentKey(path, []byte("replacement"), 0o600); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("want ErrKeyExists, got %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "existing private key" {
		t.Fatalf("existing key was modified: %q, %v", content, err)
	}
}

func TestGroupRemoveRejectsInvalidAndMissingNamesWithoutWriting(t *testing.T) {
	requireGit(t)
	svc := newTestRepo(t)
	ctx := context.Background()
	if err := svc.GroupSet(ctx, "group01", nil, ""); err != nil {
		t.Fatal(err)
	}
	head := registryHead(t, svc)
	if err := svc.GroupRemove(ctx, "../../bad"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("want ErrInvalidName, got %v", err)
	}
	if err := svc.GroupRemove(ctx, "missing"); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("want ErrGroupNotFound, got %v", err)
	}
	if registryHead(t, svc) != head {
		t.Fatal("invalid or missing group removal appended an event")
	}
}
