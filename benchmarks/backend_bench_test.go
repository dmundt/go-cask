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
				h := digestData([]byte(payload))
				if err := memBenchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
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
				h := digestData([]byte(payload))
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
				h := digestData([]byte(payload))
				if err := fsBenchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
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
				h := digestData([]byte(payload))
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
	h := digestData([]byte(payload))
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if err := memBenchmarkRoundTripWithBackend(ctx, m, payload, h); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkSummary(b, "backend-write-read/baseline/mem-1KiB", 1024)
}

func memBenchmarkRoundTripWithBackend(ctx context.Context, m *backmem.Backend, payload string, h cas.Digest) error {
	if err := m.Put(ctx, h, strings.NewReader(payload)); err != nil {
		return err
	}
	rc, err := m.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

func fsBenchmarkRoundTripWithBackend(ctx context.Context, m *fs.Backend, payload string, h cas.Digest) error {
	if err := m.Put(ctx, h, strings.NewReader(payload)); err != nil {
		return err
	}
	rc, err := m.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

func memBenchmarkRoundTrip(ctx context.Context, payload string, h cas.Digest) error {
	m := backmem.New()
	if err := m.Put(ctx, h, strings.NewReader(payload)); err != nil {
		return err
	}
	rc, err := m.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

func fsBenchmarkRoundTrip(ctx context.Context, dir, payload string, h cas.Digest) error {
	m, err := fs.New(dir)
	if err != nil {
		return err
	}
	if err := m.Put(ctx, h, strings.NewReader(payload)); err != nil {
		return err
	}
	rc, err := m.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}
