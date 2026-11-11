package chain

import (
	"path/filepath"
	"sync"
)

var writeLocks sync.Map // map[string]*sync.Mutex

// LockRepository serializes local go-git writes to a repository directory and
// returns the unlock function. Event appenders and sync merge writers share
// this lock because concurrent loose-object renames can fail on Windows.
// It is only an in-process safeguard; ref CAS and bounded retries remain
// necessary for other processes (docs/03-sync-and-concurrency.md「クラッシュ
// 安全性とローカル競合」). Callers must not acquire it recursively.
func LockRepository(dir string) func() {
	key := dir
	if abs, err := filepath.Abs(dir); err == nil {
		key = abs
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	v, _ := writeLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
