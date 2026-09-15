package benchmark_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
	sha512_256 "github.com/dmundt/go-cask/cas/hash/sha512_256"
)

func BenchmarkHashPackageDigest(b *testing.B) {
	for _, sz := range benchSizes {
		for _, hasher := range benchHashers {
			b.Run(fmt.Sprintf("%s/%s", hasher.name, sz.name), func(b *testing.B) {
				h := hasher.new()
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; b.Loop(); i++ {
					payload := []byte(benchText(sz.size, i))
					if _, err := h.Digest(bytes.NewReader(payload)); err != nil {
						b.Fatal(err)
					}
				}
				benchmarkSummary(b, fmt.Sprintf("hash/%s", hasher.name), sz.size)
			})
		}
	}
}

func BenchmarkHashPackageDigestBaseline(b *testing.B) {
	h := sha256.New()
	payload := []byte(benchText(1024, 0))
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := h.Digest(bytes.NewReader(payload)); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "hash-baseline/sha256", 1024)
}

func BenchmarkHashPackageParse(b *testing.B) {
	valid := map[string]string{
		"sha256":     "sha256:" + strings.Repeat("ab", 32),
		"sha512":     "sha512:" + strings.Repeat("cd", 64),
		"sha512_256": "sha512_256:" + strings.Repeat("ef", 32),
	}
	invalid := map[string]string{
		"sha256":     "sha256:not-hex",
		"sha512":     "sha512:not-hex",
		"sha512_256": "sha512_256:not-hex",
	}
	for _, hasher := range []struct {
		name  string
		parse func(string) (interface{ String() string }, error)
	}{
		{name: "sha256", parse: func(s string) (interface{ String() string }, error) { return sha256.Parse(s) }},
		{name: "sha512", parse: func(s string) (interface{ String() string }, error) { return sha512.Parse(s) }},
		{name: "sha512_256", parse: func(s string) (interface{ String() string }, error) { return sha512_256.Parse(s) }},
	} {
		b.Run(hasher.name+"/valid", func(b *testing.B) {
			v := valid[hasher.name]
			b.SetBytes(int64(len(v)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := hasher.parse(v); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, fmt.Sprintf("hash/parse/%s/valid", hasher.name), len(v))
		})
		b.Run(hasher.name+"/invalid", func(b *testing.B) {
			v := invalid[hasher.name]
			b.SetBytes(int64(len(v)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				if _, err := hasher.parse(v); err == nil {
					b.Fatal("invalid digest unexpectedly parsed")
				}
			}
			benchmarkSummary(b, fmt.Sprintf("hash/parse/%s/invalid", hasher.name), len(v))
		})
	}
}
