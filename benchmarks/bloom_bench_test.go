package benchmark_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/bloom"
	"github.com/dmundt/go-cask/cas/bloom/counting"
	"github.com/dmundt/go-cask/cas/bloom/persistent"
	"github.com/dmundt/go-cask/cas/bloom/standard"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

func bloomDigest(i int) cas.Digest {
	return sha256.Of(fmt.Appendf(nil, "bloom-%08d", i))
}

func bloomDigests(n int) []cas.Digest {
	items := make([]cas.Digest, n)
	for i := range items {
		items[i] = bloomDigest(i)
	}
	return items
}

func BenchmarkBloomStandardAdd(b *testing.B) {
	items := bloomDigests(128 * 1024)
	f, err := standard.New(uint64(len(items)), 0.01)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		f.Add(items[i%len(items)])
	}
}

func BenchmarkBloomStandardContainsHit(b *testing.B) {
	items := bloomDigests(128 * 1024)
	f, err := standard.New(uint64(len(items)), 0.01)
	if err != nil {
		b.Fatal(err)
	}
	for _, d := range items {
		f.Add(d)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = f.Contains(items[i%len(items)])
	}
}

func BenchmarkBloomStandardContainsHitBaseline(b *testing.B) {
	items := bloomDigests(128 * 1024)
	f, err := standard.New(uint64(len(items)), 0.01)
	if err != nil {
		b.Fatal(err)
	}
	for _, d := range items {
		f.Add(d)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = f.Contains(items[0])
	}
	benchmarkSummary(b, "bloom/baseline/standard-contains-hit", len(items))
}

func BenchmarkBloomStandardContainsMiss(b *testing.B) {
	items := bloomDigests(128 * 1024)
	f, err := standard.New(uint64(len(items)), 0.01)
	if err != nil {
		b.Fatal(err)
	}
	for _, d := range items {
		f.Add(d)
	}
	miss := bloomDigest(1 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = f.Contains(miss)
	}
}

func BenchmarkBloomCountingAddRemove(b *testing.B) {
	items := bloomDigests(128 * 1024)
	f, err := counting.New(uint64(len(items)), 0.01, 8)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		d := items[i%len(items)]
		f.Add(d)
		f.Remove(d)
	}
}

func BenchmarkBloomPersistentAdd(b *testing.B) {
	path := filepath.Join(b.TempDir(), "persistent.bloom")
	f, err := persistent.New(path, 128*1024, 0.01)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}()
	items := bloomDigests(128 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		f.Add(items[i%len(items)])
	}
}

func BenchmarkBloomPersistentContains(b *testing.B) {
	path := filepath.Join(b.TempDir(), "persistent.bloom")
	f, err := persistent.New(path, 128*1024, 0.01)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	}()
	items := bloomDigests(128 * 1024)
	for _, d := range items {
		f.Add(d)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = f.Contains(items[i%len(items)])
	}
}

func BenchmarkBloomGuardExists(b *testing.B) {
	ctx := context.Background()
	backend := mem.New()
	items := bloomDigests(128 * 1024)
	filter, err := standard.New(uint64(len(items)), 0.01)
	if err != nil {
		b.Fatal(err)
	}
	guard, err := bloom.NewGuard(backend, filter)
	if err != nil {
		b.Fatal(err)
	}
	for _, d := range items {
		if err := guard.Put(ctx, d, bytes.NewReader([]byte("payload"))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_, err := guard.Exists(ctx, items[i%len(items)])
		if err != nil {
			b.Fatal(err)
		}
	}
}
