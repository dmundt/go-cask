package benchmark_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/internal/test"
)

func BenchmarkBackendWriteRead(b *testing.B) {
	ctx := context.Background()
	for _, sz := range benchSizes {
		b.Run(fmt.Sprintf("mem/steady-state/%s", sz.name), func(b *testing.B) {
			m := backmem.New()
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				payload := benchText(sz.size, i)
				h := test.DigestData([]byte(payload))
				if err := benchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "backend-write-read/mem/steady-state", sz.size)
		})
		b.Run(fmt.Sprintf("mem/setup/cold-start/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				payload := benchText(sz.size, i)
				h := test.DigestData([]byte(payload))
				if err := memBenchmarkRoundTrip(ctx, payload, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "backend-write-read/mem/setup/cold-start", sz.size)
		})
		b.Run(fmt.Sprintf("fs/steady-state/%s", sz.name), func(b *testing.B) {
			dir := b.TempDir()
			m, err := fs.New(dir)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				payload := benchText(sz.size, i)
				h := test.DigestData([]byte(payload))
				if err := benchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "backend-write-read/fs/steady-state", sz.size)
		})
		b.Run(fmt.Sprintf("fs/setup/cold-start/%s", sz.name), func(b *testing.B) {
			b.SetBytes(int64(sz.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				payload := benchText(sz.size, i)
				h := test.DigestData([]byte(payload))
				if err := fsBenchmarkRoundTrip(ctx, b.TempDir(), payload, h); err != nil {
					b.Fatal(err)
				}
			}
			benchmarkSummary(b, "backend-write-read/fs/setup/cold-start", sz.size)
		})
	}
}

func BenchmarkBackendWriteReadBaseline(b *testing.B) {
	ctx := context.Background()
	m := backmem.New()
	payload := benchText(1024, 0)
	h := test.DigestData([]byte(payload))
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if err := benchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "backend-write-read/baseline/mem-1KiB", 1024)
}

func benchmarkRoundTripWithBackend(ctx context.Context, backend cas.Backend, payload string, h cas.Digest) error {
	if err := backend.Put(ctx, h, strings.NewReader(payload)); err != nil {
		return err
	}
	rc, err := backend.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

// memBenchmarkRoundTrip measures the cold-start shape: a fresh in-memory
// backend per measured operation.
func memBenchmarkRoundTrip(ctx context.Context, payload string, h cas.Digest) error {
	return benchmarkRoundTripWithBackend(ctx, backmem.New(), payload, h)
}

// fsBenchmarkRoundTrip is memBenchmarkRoundTrip over a fresh filesystem store.
func fsBenchmarkRoundTrip(ctx context.Context, dir, payload string, h cas.Digest) error {
	m, err := fs.New(dir)
	if err != nil {
		return err
	}
	return benchmarkRoundTripWithBackend(ctx, m, payload, h)
}
