package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// lockFileName is the lock file's name inside the store directory. It lives
// at the store root (not under <algo>/), so the backend's List/Stats never
// see it (cas-core §4.4).
const lockFileName = ".cask.lock"

// storeLock is an exclusive, advisory cross-process lock on a store
// directory. The cas library coordinates threads within ONE process
// (cas-core §6); this lock extends that contract across OS processes at the
// application layer: mutating operations (put, gc, prune, clean) and the
// viewer (web) take it exclusively, so two processes never mutate the same
// store concurrently. Reads never lock — they are lock-free and safe
// concurrently.
//
// The lock is a plain file created atomically with O_CREATE|O_EXCL (the
// caller's PID and start time are written inside for diagnosis). It is
// removed on release; a crash leaves a stale file, which the message tells
// the operator to delete when no such process is running.
type storeLock struct {
	path string
}

// acquireStoreLock takes the exclusive lock for dir, refusing (with the
// holder's PID) if another process holds it.
func acquireStoreLock(dir string) (*storeLock, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			holder := readLockHolder(path)
			return nil, fmt.Errorf("store %q is locked by %s (mutating ops take an exclusive lock, cas-core §6); stop that process, or remove %s if it is stale", dir, holder, path)
		}
		return nil, fmt.Errorf("lock store: %w", err)
	}
	fmt.Fprintf(f, "pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("lock store: %w", err)
	}
	return &storeLock{path: path}, nil
}

// readLockHolder returns a human description of the locking process from the
// lock file, or "an unknown process" if it cannot be parsed.
func readLockHolder(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "an unknown process"
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if pid, ok := strings.CutPrefix(line, "pid="); ok {
			if n, err := strconv.Atoi(pid); err == nil {
				return fmt.Sprintf("process %d", n)
			}
		}
	}
	return "an unknown process"
}

// release removes the lock file.
func (l *storeLock) release() {
	if l != nil && l.path != "" {
		os.Remove(l.path)
	}
}
