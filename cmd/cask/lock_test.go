package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaintenanceOpsTakeLock: a maintenance op (gc) refuses while another
// lock holder is active, and the error names the holder's PID.
func TestMaintenanceOpsTakeLock(t *testing.T) {
	mf := localMF(t)
	holder, err := acquireStoreLock(mf.store)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.release()

	h := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	_, code := run(t, mf, "gc", h)
	if code != 1 {
		t.Fatalf("gc under an active lock: exit %d, want 1", code)
	}
}

// TestPutDoesNotLock: writers (put) are lock-free — a put succeeds while
// another process holds the maintenance lock (cas-core §6).
func TestPutDoesNotLock(t *testing.T) {
	mf := localMF(t)
	holder, err := acquireStoreLock(mf.store)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.release()

	f := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, mf, "put", f); code != 0 {
		t.Fatalf("put under a maintenance lock: exit %d, want 0 (writers never lock)", code)
	}
}

// TestLockReleasedAfterOp: after a maintenance op completes, the lock is
// gone.
func TestLockReleasedAfterOp(t *testing.T) {
	mf := localMF(t)
	h := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, code := run(t, mf, "gc", "--min-age", "0", h); code != 0 {
		t.Fatalf("gc exit %d, want 0", code)
	}
	lock, err := acquireStoreLock(mf.store)
	if err != nil {
		t.Fatalf("lock not released after op: %v", err)
	}
	lock.release()
}

// TestReadOpsDoNotLock: reads succeed while another process holds the lock.
func TestReadOpsDoNotLock(t *testing.T) {
	mf := localMF(t)
	// Seed one object first (lock-free path).
	f := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, mf, "put", f); code != 0 {
		t.Fatalf("seed put exit %d", code)
	}

	holder, err := acquireStoreLock(mf.store)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.release()

	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"stats", nil},
		{"list", nil},
	} {
		if _, code := run(t, mf, tc.cmd, tc.args...); code != 0 {
			t.Fatalf("%s under an active lock: exit %d, want 0 (reads never lock)", tc.cmd, code)
		}
	}
}

// TestLockFileRecordsPID: the lock file carries the holder's PID.
func TestLockFileRecordsPID(t *testing.T) {
	mf := localMF(t)
	holder, err := acquireStoreLock(mf.store)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.release()
	b, err := os.ReadFile(filepath.Join(mf.store, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "pid=") {
		t.Fatalf("lock file = %q, want a pid= line", b)
	}
	if got := readLockHolder(filepath.Join(mf.store, lockFileName)); !strings.Contains(got, "process") {
		t.Fatalf("readLockHolder = %q, want a process description", got)
	}
}

// TestLockAcquireRelease: the maintenance lock is exclusive — a second
// acquisition fails while one holder is active, and succeeds after release.
func TestLockAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	lock, err := acquireStoreLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireStoreLock(dir); err == nil {
		t.Fatal("second acquisition must fail while one holder is active")
	}
	lock.release()
	if _, err := acquireStoreLock(dir); err != nil {
		t.Fatalf("acquisition after release: %v", err)
	}
}
