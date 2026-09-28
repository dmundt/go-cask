package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// benchmarkPayloadSize is the object a benchmark Put writes: 4 KiB, small
// enough that the index rewrite — not the payload — is the work being measured.
const benchmarkPayloadSize = 4 << 10

// BenchmarkPackIndexRewrite measures one packed Put on a store whose index
// already holds 10, 1 000 or 10 000 entries (go-cask#369).
//
// persistIndex marshals and rewrites the whole index on every packed Put, so the
// per-Put cost is O(index entries): watch B/op and allocs/op grow roughly
// linearly with `entries` at a fixed 4 KiB payload. The benchmark exists so that
// growth is a number in the record rather than a claim, and so a regression in
// the rewrite path is visible.
//
// The setup seeds the index directly rather than calling Put N times, because a
// real prefill would itself pay this O(N) cost per Put (10 000 entries through
// Put is quadratic — minutes of work). The seeded records name one real 4 KiB
// payload in a real pack file, and the reopened backend loads them through the
// same loadIndex a store does, so the measured Put — spool, loose write, pack
// append, full index marshal — is the production path. The payload is re-Put, so
// the index size stays fixed while every Put still rewrites all of it.
func BenchmarkPackIndexRewrite(b *testing.B) {
	for _, entries := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("entries=%d", entries), func(b *testing.B) {
			ctx := context.Background()
			base := filepath.Join(b.TempDir(), "pack-index-rewrite")

			// One real Put establishes the pack file and a valid record shape.
			seedBackend, err := New(base, WithEnabled(), WithPackMaxEntries(0))
			if err != nil {
				b.Fatalf("seed backend: %v", err)
			}
			payload := bytes.Repeat([]byte("x"), benchmarkPayloadSize)
			digest := sha256.Of(payload)
			if err := seedBackend.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
				b.Fatalf("seed Put: %v", err)
			}
			seedRecord := seedBackend.index[string(digest)]
			if err := seedBackend.Close(); err != nil {
				b.Fatalf("seed Close: %v", err)
			}

			// The index a store of `entries` packed objects would have on disk:
			// distinct digests, every record naming that real payload.
			seeded := manifest{Entries: make(map[string]packRecord, entries)}
			seeded.Entries[digest.String()] = seedRecord
			for i := range entries - 1 {
				d := sha256.Of(fmt.Appendf(nil, "seed %d", i))
				seeded.Entries[d.String()] = seedRecord
			}
			data, err := json.Marshal(seeded)
			if err != nil {
				b.Fatalf("encode seeded index: %v", err)
			}
			if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), data, 0o644); err != nil {
				b.Fatalf("write seeded index: %v", err)
			}

			backend, err := New(base, WithEnabled(), WithPackMaxEntries(0))
			if err != nil {
				b.Fatalf("reopen with %d index entries: %v", entries, err)
			}
			defer func() {
				if err := backend.Close(); err != nil {
					b.Errorf("Close: %v", err)
				}
			}()
			if got := len(backend.index); got != entries {
				b.Fatalf("loaded index holds %d entries, want %d", got, entries)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := backend.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
					b.Fatalf("Put: %v", err)
				}
			}
			b.StopTimer()
			if got := len(backend.index); got != entries {
				b.Fatalf("index holds %d entries after the loop, want %d", got, entries)
			}
		})
	}
}
