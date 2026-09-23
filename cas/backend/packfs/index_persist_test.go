package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestPackIndexPersistsAfterPutClose pins the write path's durability contract:
// a Put followed by Close leaves the pack index on disk with no separate flush
// call anywhere, and the index survives the JSON round trip into the next
// process.
//
// The index key is asserted to be the digest's hex form rather than its raw
// bytes. encoding/json replaces invalid UTF-8 in a map key with U+FFFD, so a raw
// digest key — sha256 output is arbitrary binary — comes back corrupted: the
// index would hold keys no lookup matches, List would report phantom digests,
// and Stats would count every object twice (loose plus an unmatchable index
// entry).
func TestPackIndexPersistsAfterPutClose(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "pack-index")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("packed object")
	digest := sha256.Of(payload)
	if err := backend.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The index is on disk after Put + Close — nothing flushed it elsewhere.
	indexPath := filepath.Join(base, "packs", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read pack index after Put + Close: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode pack index %q: %v", data, err)
	}
	record, ok := m.Entries[digest.String()]
	if !ok {
		t.Fatalf("pack index keys = %v, want the hex digest %s", keysOf(m.Entries), digest)
	}
	if record.Size != int64(len(payload)) {
		t.Fatalf("indexed size = %d, want %d", record.Size, len(payload))
	}
	if _, err := os.Stat(record.Pack); err != nil {
		t.Fatalf("indexed pack file %q: %v", record.Pack, err)
	}

	// A fresh backend over the same directory — the next CLI invocation — reads
	// the object back and reports exactly the digests that were written.
	reopened, err := New(base, WithEnabled())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if len(reopened.index) != 1 {
		t.Fatalf("reopened index holds %d entries, want 1 (hex keys must round-trip)", len(reopened.index))
	}
	if _, ok := reopened.index[string(digest)]; !ok {
		t.Fatalf("reopened index = %v, want the raw digest %s", keysOf(reopened.index), digest)
	}
	list, err := reopened.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || !list[0].Equal(digest) {
		t.Fatalf("List after reopen = %v, want exactly [%s]", list, digest)
	}
	stats, err := reopened.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.ObjectCount != 1 || stats.TotalSize != int64(len(payload)) {
		t.Fatalf("Stats after reopen = %+v, want 1 object of %d bytes", stats, len(payload))
	}
	rc, err := reopened.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	got, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read after reopen = (%v, %v)", readErr, closeErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get after reopen = %q, want %q", got, payload)
	}

	// The refreshed index still names the same object: a load that dropped or
	// rewrote an entry would show up as a second write changing the digest set.
	if err := reopened.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatalf("re-Put after reopen: %v", err)
	}
	list, err = reopened.List(ctx)
	if err != nil {
		t.Fatalf("List after re-Put: %v", err)
	}
	if len(list) != 1 || !list[0].Equal(digest) {
		t.Fatalf("List after re-Put = %v, want exactly [%s]", list, digest)
	}
}

// TestLoadIndexDropsRawDigestKeys: an index written by a build that keyed the
// manifest by raw digest bytes (a form JSON cannot round-trip) names no
// addressable object, so loading it drops those entries instead of reporting
// corrupted keys — and the loose copy keeps the object readable.
func TestLoadIndexDropsRawDigestKeys(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "raw-keys")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("still readable")
	digest := sha256.Of(payload)
	if err := backend.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Rewrite the manifest in the legacy raw-key form, as the old code would
	// have marshalled it.
	legacy, err := json.Marshal(manifest{Entries: map[string]packRecord{
		string(digest): {Pack: filepath.Join(base, "packs", "current.pack"), Offset: 0, Size: int64(len(payload))},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(base, WithEnabled())
	if err != nil {
		t.Fatalf("reopen with a raw-key manifest: %v", err)
	}
	defer reopened.Close()
	for key := range reopened.index {
		parsed, err := cas.ParseDigest(key)
		if err != nil {
			t.Fatalf("reopened index kept the unusable key %q", key)
		}
		if !parsed.Equal(digest) {
			t.Fatalf("reopened index key %s, want %s or nothing", parsed, digest)
		}
	}
	rc, err := reopened.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get after a raw-key manifest: %v", err)
	}
	got, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read after a raw-key manifest = (%v, %v)", readErr, closeErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get after a raw-key manifest = %q, want %q", got, payload)
	}
}

// keysOf lists a pack index's keys for a failure message.
func keysOf(entries map[string]packRecord) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	return keys
}
