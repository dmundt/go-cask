package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// errProbeTransient stands in for the Windows replace failure ("Access is
// denied" / "being used by another process") a concurrent reader or writer of
// index.json causes (go-cask#369).
var errProbeTransient = errors.New("probe: index rename is transiently refused")

// TestPersistIndexRetriesTransientRename pins the retry the index publication
// needs: the pack index is rewritten once per packed Put, so a momentary refusal
// to replace it must not fail the Put. The rename seam fails three times and
// then publishes, and the index on disk must end up complete.
func TestPersistIndexRetriesTransientRename(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "retry-rename")

	op := realOps()
	attempts := 0
	op.rename = func(oldPath, newPath string) error {
		attempts++
		if attempts <= 3 {
			return errProbeTransient
		}
		return os.Rename(oldPath, newPath)
	}

	backend, err := newWithOps(base, op, WithEnabled())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer backend.Close()

	payload := []byte("retried index write")
	digest := sha256.Of(payload)
	if err := backend.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put over a transiently failing rename: %v", err)
	}
	if attempts != 4 {
		t.Fatalf("rename attempts = %d, want 4 (three refusals, then the publish)", attempts)
	}

	// The published index is the real manifest, not a partial or stale one.
	data, err := os.ReadFile(filepath.Join(base, "packs", "index.json"))
	if err != nil {
		t.Fatalf("read published index: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode published index: %v", err)
	}
	if _, ok := m.Entries[digest.String()]; !ok {
		t.Fatalf("published index keys = %v, want %s", keysOf(m.Entries), digest)
	}
}

// TestPersistIndexReportsAPersistentRenameFailure is the other side of the
// retry: a rename that never succeeds is reported (wrapped, so errors.Is finds
// the cause), the Put fails rather than silently leaving the index behind, and
// the scratch file is cleaned up instead of accumulating one temp per Put.
func TestPersistIndexReportsAPersistentRenameFailure(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "refused-rename")

	op := realOps()
	attempts := 0
	op.rename = func(string, string) error {
		attempts++
		return errProbeTransient
	}

	backend, err := newWithOps(base, op, WithEnabled())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer backend.Close()

	payload := []byte("refused index write")
	digest := sha256.Of(payload)
	err = backend.Put(ctx, digest, bytes.NewReader(payload))
	if err == nil {
		t.Fatal("Put over a rename that never succeeds must fail")
	}
	if !errors.Is(err, errProbeTransient) {
		t.Fatalf("Put error = %v, want it to wrap the rename failure", err)
	}
	if attempts != 20 {
		t.Fatalf("rename attempts = %d, want the full retry budget of 20", attempts)
	}
	if leftovers, err := filepath.Glob(filepath.Join(base, "packs", "index-*.tmp")); err != nil {
		t.Fatalf("glob scratch files: %v", err)
	} else if len(leftovers) != 0 {
		t.Fatalf("failed publish left scratch files behind: %v", leftovers)
	}
}
