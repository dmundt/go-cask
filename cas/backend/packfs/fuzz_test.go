package packfs

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func FuzzPackRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{{0x01}, []byte("hello"), bytes.Repeat([]byte{0xff}, 32)} {
		f.Add(seed, []byte("payload"))
	}
	f.Fuzz(func(t *testing.T, digestSeed []byte, payload []byte) {
		if len(digestSeed) == 0 {
			digestSeed = []byte{0x01}
		}
		ctx := context.Background()
		base := filepath.Join(t.TempDir(), "store")
		b, err := New(base, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()

		d := cas.NewDigest(digestSeed)
		if err := b.Put(ctx, d, bytes.NewReader(payload)); err != nil {
			t.Fatalf("Put() = %v, want nil", err)
		}
		if ok, err := b.Exists(ctx, d); err != nil || !ok {
			t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
		}
		reader, err := b.Get(ctx, d)
		if err != nil {
			t.Fatalf("Get() = %v, want nil", err)
		}
		defer reader.Close()
		got, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() = %v, want nil", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("Get() = %q, want %q", got, payload)
		}
		list, err := b.List(ctx)
		if err != nil {
			t.Fatalf("List() = %v, want nil", err)
		}
		if !containsDigest(list, d) {
			t.Fatalf("List() = %v, want digest %s present", list, d)
		}
		stats, err := b.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats() = %v, want nil", err)
		}
		if stats.ObjectCount == 0 && len(payload) > 0 {
			t.Fatal("Stats().ObjectCount = 0, want at least 1")
		}
		if err := b.Delete(ctx, d); err != nil {
			t.Fatalf("Delete() = %v, want nil", err)
		}
		if ok, err := b.Exists(ctx, d); err != nil || ok {
			t.Fatalf("Exists() after Delete = (%v, %v), want (false, nil)", ok, err)
		}
	})
}

func containsDigest(list []cas.Digest, want cas.Digest) bool {
	for _, d := range list {
		if d.Equal(want) {
			return true
		}
	}
	return false
}
