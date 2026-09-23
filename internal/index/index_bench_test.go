package index

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

func BenchmarkBuildSnapshotScale(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("objects=%d", count), func(b *testing.B) {
			backend, err := fs.New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			for i := range count {
				payload := []byte(fmt.Sprintf("%06d", i))
				envelope := append([]byte{1, byte(6)}, payload...)
				digest := sha256.Of(envelope)
				if err := backend.Put(ctx, digest, bytes.NewReader(envelope)); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := BuildSnapshot(ctx, backend); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
