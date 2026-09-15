package benchmark_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

func BenchmarkCodecPackageMarshalUnmarshal(b *testing.B) {
	for _, sz := range benchSizes {
		for _, codec := range benchCodecs {
			name := codec.name
			if name == "binary" {
				name = "binary-custom"
			}
			b.Run(fmt.Sprintf("%s/%s", name, sz.name), func(b *testing.B) {
				c := codec.new()
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					note := benchNoteWithSeed(sz.size, i)
					data, err := c.Marshal(note)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := c.Unmarshal(data); err != nil {
						b.Fatal(err)
					}
				}
				benchmarkSummary(b, fmt.Sprintf("codec/%s", name), sz.size)
			})
		}
	}
}

func BenchmarkCodecPackageRoundTrip(b *testing.B) {
	for _, sz := range benchSizes {
		for _, codec := range benchCodecs {
			for _, hasher := range benchHashers {
				name := codec.name
				if name == "binary" {
					name = "binary-custom"
				}
				b.Run(fmt.Sprintf("%s/%s/%s", name, hasher.name, sz.name), func(b *testing.B) {
					ctx := context.Background()
					s := cas.New(mem.New(), codec.new(), hasher.new())
					b.SetBytes(int64(sz.size))
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; b.Loop(); i++ {
						note := benchNoteWithSeed(sz.size, i)
						h, err := s.Put(ctx, note)
						if err != nil {
							b.Fatal(err)
						}
						if _, err := s.Get(ctx, h); err != nil {
							b.Fatal(err)
						}
					}
					benchmarkSummary(b, fmt.Sprintf("codec-stack/%s/%s", name, hasher.name), sz.size)
				})
			}
		}
	}
}

func BenchmarkCodecRoundTripBaseline(b *testing.B) {
	ctx := context.Background()
	c := jsoncodec.New[testNote]()
	s := cas.New(mem.New(), c, sha256.New())
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		note := benchNoteWithSeed(1024, i)
		h, err := s.Put(ctx, note)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "codec-baseline/json-sha256", 1024)
}
