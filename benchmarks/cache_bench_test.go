package benchmark_test

import (
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	membackend "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/cache/lru"
	cachemem "github.com/dmundt/go-cask/cas/cache/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

func BenchmarkCacheMemoryGet(b *testing.B) {
	ctx := context.Background()
	store := cas.New(membackend.New(), jsoncodec.New[testNote](), sha256.New())
	count := 256
	digests := make([]cas.Digest, 0, count)
	for i := 0; i < count; i++ {
		d, err := store.Put(ctx, testNote{Title: benchTitle("cache-", i)})
		if err != nil {
			b.Fatal(err)
		}
		digests = append(digests, d)
	}
	cache := cachemem.New(store)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := cache.Get(ctx, digests[i%len(digests)]); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "cache/memory-get", count)
}

func BenchmarkCacheMemoryGetBaseline(b *testing.B) {
	ctx := context.Background()
	store := cas.New(membackend.New(), jsoncodec.New[testNote](), sha256.New())
	d, err := store.Put(ctx, testNote{Title: "cache-baseline"})
	if err != nil {
		b.Fatal(err)
	}
	cache := cachemem.New(store)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := cache.Get(ctx, d); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "cache/baseline/memory-get", 1)
}

func BenchmarkCacheLRUGet(b *testing.B) {
	ctx := context.Background()
	store := cas.New(membackend.New(), jsoncodec.New[testNote](), sha256.New())
	count := 256
	digests := make([]cas.Digest, 0, count)
	for i := 0; i < count; i++ {
		d, err := store.Put(ctx, testNote{Title: benchTitle("lru-", i)})
		if err != nil {
			b.Fatal(err)
		}
		digests = append(digests, d)
	}
	cache, err := lru.New(store, 64)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := cache.Get(ctx, digests[i%len(digests)]); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "cache/lru-get", count)
}
