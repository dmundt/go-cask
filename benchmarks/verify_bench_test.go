package benchmark_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

func BenchmarkVerify(b *testing.B) {
	ctx := context.Background()
	data := strings.Repeat("verify", 1024)
	for _, tc := range []struct {
		name   string
		mutate func(cas.Backend, cas.Digest) cas.Digest
	}{
		{name: "valid", mutate: func(raw cas.Backend, h cas.Digest) cas.Digest { return h }},
		{name: "corrupt", mutate: func(raw cas.Backend, h cas.Digest) cas.Digest {
			bad := sha256.Of([]byte("corrupt" + data))
			if err := raw.Put(ctx, bad, strings.NewReader(data)); err != nil {
				panic(err)
			}
			return bad
		}},
		{name: "missing", mutate: func(raw cas.Backend, h cas.Digest) cas.Digest {
			return sha256.Of([]byte("missing" + data))
		}},
	} {
		b.Run("verify/"+tc.name, func(b *testing.B) {
			raw, err := fs.New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			h := digestData([]byte(data))
			if err := raw.Put(ctx, h, strings.NewReader(data)); err != nil {
				b.Fatal(err)
			}
			bad := tc.mutate(raw, h)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if tc.name == "valid" {
					if err := raw.Verify(ctx, bad, sha256.New()); err != nil {
						b.Fatal(err)
					}
					continue
				}
				if err := raw.Verify(ctx, bad, sha256.New()); err == nil {
					b.Fatal("expected verify failure for invalid object")
				}
			}
			benchmarkSummary(b, "verify/"+tc.name, len(data))
		})
	}
}

func BenchmarkVerifyBaseline(b *testing.B) {
	ctx := context.Background()
	data := strings.Repeat("verify", 1024)
	raw, err := fs.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	h := digestData([]byte(data))
	if err := raw.Put(ctx, h, strings.NewReader(data)); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if err := raw.Verify(ctx, h, sha256.New()); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "verify/baseline/valid", len(data))
}

func BenchmarkParseDigest(b *testing.B) {
	valid := "sha256:" + strings.Repeat("ab", 32)
	invalid := "sha256:not-hex"
	b.Run("valid", func(b *testing.B) {
		b.SetBytes(int64(len(valid)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			if _, err := sha256.Parse(valid); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkSummary(b, "parse-digest/valid", len(valid))
	})
	b.Run("invalid", func(b *testing.B) {
		b.SetBytes(int64(len(invalid)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			if _, err := sha256.Parse(invalid); err == nil {
				b.Fatal("invalid digest unexpectedly parsed")
			}
		}
		benchmarkSummary(b, "parse-digest/invalid", len(invalid))
	})
}

func BenchmarkParallelPutGet(b *testing.B) {
	ctx := context.Background()
	store := cas.New(mem.New(), jsoncodec.New[testNote](), sha256.New())
	const objects = 64
	const hotSetSize = benchmarkHotSetSize
	const coldRatio = benchmarkMixedColdRatio
	// Hot set: 16 items kept hot in cache; cold path: every 10th op creates a new
	// object and reads it once to model a realistic mixed access pattern.
	var digests []cas.Digest
	for i := 0; i < objects; i++ {
		h, err := store.Put(ctx, testNote{Title: fmt.Sprintf("obj-%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		digests = append(digests, h)
	}
	warm := make([]cas.Digest, hotSetSize)
	copy(warm, digests[:hotSetSize])
	b.SetBytes(256)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i uint64
		for pb.Next() {
			idx := int(i % uint64(objects))
			if i%coldRatio == 0 {
				h, err := store.Put(ctx, testNote{Title: fmt.Sprintf("parallel-%d", i)})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := store.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
			} else {
				if _, err := store.Get(ctx, warm[idx%len(warm)]); err != nil {
					b.Fatal(err)
				}
			}
			i++
		}
	})
	benchmarkSummary(b, "parallel-put-get/mixed", 256)
}
