package benchmark_test

// Benchmarks per performance §5: every case reports allocations and bytes.
// Store-level benchmarks run against the mem backend (deterministic, no disk
// noise); the FSBackend cases cover disk behavior separately.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// testNote is a small Object[T] used to size store benchmarks.
type testNote struct {
	Title string
	Body  string
}

func (testNote) Type() string           { return "note@1" }
func (testNote) References() []cas.Hash { return nil }

// hashData computes the content address of data.
func hashData(data []byte) cas.Hash {
	return cas.HashBytes(data)
}

func benchNote(size int) testNote {
	return testNote{Title: strings.Repeat("a", size/2), Body: strings.Repeat("b", size/2)}
}

var benchSizes = []struct {
	name string
	size int
}{
	{"64B", 64},
	{"1KiB", 1024},
	{"1MiB", 1024 * 1024},
}

func BenchmarkStorePut(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(mem.New(), jsoncodec.New[testNote]())
			note := benchNote(sz.size)
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Put(ctx, note); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStoreGet(b *testing.B) {
	for _, sz := range benchSizes {
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			s := cas.New(mem.New(), jsoncodec.New[testNote]())
			h, err := s.Put(ctx, benchNote(sz.size))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Get(ctx, h); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFSBackendPut(b *testing.B) {
	for _, layout := range []struct {
		name string
		opts []backend.Option
	}{
		{"flat", []backend.Option{fs.WithFanOut(0), fs.WithFanLevels(0)}},
		{"fanout-2-1", nil},
	} {
		for _, sz := range []struct {
			name string
			size int
		}{
			{"64B", 64},
			{"1KiB", 1024},
		} {
			b.Run(layout.name+"/"+sz.name, func(b *testing.B) {
				ctx := context.Background()
				s, err := fs.New(b.TempDir(), layout.opts...)
				if err != nil {
					b.Fatal(err)
				}
				// Distinct content per iteration: each Put creates a new
				// object (a realistic write workload) rather than
				// overwriting one hash. The counter spans 8 bytes, so the
				// content does not repeat within any realistic b.N.
				data := []byte(strings.Repeat("x", sz.size))
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					binary.BigEndian.PutUint64(data, uint64(i))
					h := hashData(data)
					if err := s.Put(ctx, h, bytes.NewReader(data)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkFSBackendGet(b *testing.B) {
	for _, layout := range []struct {
		name string
		opts []backend.Option
	}{
		{"flat", []backend.Option{fs.WithFanOut(0), fs.WithFanLevels(0)}},
		{"fanout-2-1", nil},
	} {
		for _, sz := range []struct {
			name string
			size int
		}{
			{"64B", 64},
			{"1KiB", 1024},
		} {
			b.Run(layout.name+"/"+sz.name, func(b *testing.B) {
				ctx := context.Background()
				s, err := fs.New(b.TempDir(), layout.opts...)
				if err != nil {
					b.Fatal(err)
				}
				h := hashData(make([]byte, sz.size))
				data := strings.Repeat("x", sz.size)
				if err := s.Put(ctx, h, strings.NewReader(data)); err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(sz.size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					rc, err := s.Get(ctx, h)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := io.Copy(io.Discard, rc); err != nil {
						b.Fatal(err)
					}
					rc.Close()
				}
			})
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[testNote]())
	note := benchNote(1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h, err := s.Put(ctx, note)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerify(b *testing.B) {
	ctx := context.Background()
	s, err := fs.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	data := strings.Repeat("verify", 1024)
	h := hashData([]byte(data))
	if err := s.Put(ctx, h, strings.NewReader(data)); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Verify(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseHash(b *testing.B) {
	valid := "sha256:" + strings.Repeat("ab", 32)
	invalid := "sha256:not-hex"
	b.Run("valid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := cas.ParseHash(valid); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("invalid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cas.ParseHash(invalid)
		}
	})
}

// BenchmarkParallelPutGet exercises the lock-free read path under
// concurrency (performance §2).
func BenchmarkParallelPutGet(b *testing.B) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[testNote]())
	const objects = 64
	var hashes []cas.Hash
	for i := 0; i < objects; i++ {
		h, err := s.Put(ctx, testNote{Title: fmt.Sprintf("obj-%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			h := hashes[i%objects]
			i++
			if _, err := s.Get(ctx, h); err != nil {
				b.Fatal(err)
			}
		}
	})
}
