package memory

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestMemoryBackendListSortsByDigestBytes pins List's ordering: the raw digest
// bytes are compared (hex order is byte order — the hex alphabet is ordinal and
// nibble-preserving), so List returns a deterministic ascending order that a
// caller can binary-search and that matches sorting the rendered hex strings.
// The backend names no algorithm, so the order is the only filter-free
// guarantee List makes.
func TestMemoryBackendListSortsByDigestBytes(t *testing.T) {
	ctx := context.Background()
	b := New()
	// Insert in an order that is not the sorted order, so the comparator is
	// really exercised rather than satisfied by insertion order.
	payloads := []string{"list-c", "list-a", "list-b"}
	for _, payload := range payloads {
		if err := b.Put(ctx, sha256.Of([]byte(payload)), bytes.NewReader([]byte(payload))); err != nil {
			t.Fatalf("Put(%q): %v", payload, err)
		}
	}

	got, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payloads) {
		t.Fatalf("List() returned %d digests, want %d", len(got), len(payloads))
	}
	if !slices.IsSortedFunc(got, func(a, b cas.Digest) int { return bytes.Compare(a, b) }) {
		t.Fatalf("List() = %v, want ascending digest-byte order", got)
	}
	if !slices.IsSortedFunc(got, func(a, b cas.Digest) int { return bytes.Compare([]byte(a.String()), []byte(b.String())) }) {
		t.Fatalf("List() = %v, want an order that also sorts the rendered hex forms", got)
	}
	// The same digests, sorted by their hex rendering, are exactly what List
	// returned: the two orders cannot disagree.
	want := make([]cas.Digest, 0, len(payloads))
	for _, payload := range payloads {
		want = append(want, sha256.Of([]byte(payload)))
	}
	slices.SortFunc(want, func(a, b cas.Digest) int { return bytes.Compare(a, b) })
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("List()[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestMemoryBackendListAndStatsSkipAStrayEmptyKey pins the guard both readers
// keep for a key the public API cannot produce: cas.CheckDigest rejects the
// absent digest on every entry point, so "" is never a key a Put or a Restore
// can create — but the map is the backend's own state, and a guarded read must
// not report the absent digest as an object or count its bytes. The key is
// injected directly, the same way the corruption-recovery test installs
// tampered bytes.
func TestMemoryBackendListAndStatsSkipAStrayEmptyKey(t *testing.T) {
	ctx := context.Background()
	b := New()
	payload := []byte("real object")
	d := sha256.Of(payload)
	if err := b.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.objects[""] = []byte("stray bytes under the absent digest")
	b.mu.Unlock()

	got, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Equal(d) {
		t.Fatalf("List() = %v, want exactly [%s]", got, d)
	}

	st, err := b.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 1 || st.TotalSize != int64(len(payload)) {
		t.Fatalf("Stats() = %+v, want 1 object of %d bytes (the stray key must not be counted)", st, len(payload))
	}
}
