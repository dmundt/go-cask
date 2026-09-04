package cas

// On-demand scale probes (performance §5): how per-operation cost and
// resource use grow with the number of objects already in the store. They
// answer the capacity question "what does a store of 10^10 (ten billion)
// unique objects cost?" — a real filesystem or memory cannot hold that many
// (projection output below shows why), so run them at increasing N on your
// machine and read the scaling curve.
//
// NOT part of CI, twice over: CI runs no -bench steps at all, and every
// benchmark in this file skips unless CASK_SCALE_OBJECTS is set.
//
// Run (add -timeout 0 for large N; the FS backend uses a temp dir that is
// removed when the run finishes). Iteration-based -benchtime gives exact
// operation counts and keeps big runs bounded; -v shows each benchmark's
// projection line:
//
//	CASK_SCALE_OBJECTS=100000  go test -bench=Scale -run=^$ -benchtime=1000x -v ./cas/
//	CASK_SCALE_OBJECTS=1000000 go test -bench=Scale -run=^$ -benchtime=100x -v ./cas/   # FS: ~GBs of temp files
//
// Prefilling N objects happens before the measured loop and can take
// minutes at large N — that setup time is not part of the per-op numbers.
// Each benchmark ends with a projection line for the 10^10 target (wall
// time at the measured rate, and file bytes for the FS backend).
import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
)

// scaleTarget is the object count these probes extrapolate to:
// 10,000,000,000 (ten billion).
const scaleTarget = 10_000_000_000

// scaleObjSize is the payload size of every probe object (small objects
// maximize per-object overhead, the interesting part of scaling).
const scaleObjSize = 64

// scaleObjectCount returns the prefill size from CASK_SCALE_OBJECTS and
// skips the benchmark when it is unset (the not-in-CI gate).
func scaleObjectCount(b *testing.B) int {
	b.Helper()
	v := os.Getenv("CASK_SCALE_OBJECTS")
	if v == "" {
		b.Skip("scale probes are on-demand: set CASK_SCALE_OBJECTS=<n> (target 10^10)")
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		b.Fatalf("CASK_SCALE_OBJECTS=%q: want a positive integer", v)
	}
	return n
}

// scaleBackend pairs a name with a RawStore constructor.
type scaleBackend struct {
	name string
	new  func(tb testing.TB) RawStore
}

func scaleBackends() []scaleBackend {
	return []scaleBackend{
		{"Memory", func(tb testing.TB) RawStore { return NewMemoryRawStore() }},
		{"FS", func(tb testing.TB) RawStore {
			s, err := NewFSRawStore(tb.TempDir())
			if err != nil {
				tb.Fatal(err)
			}
			return s
		}},
	}
}

// scalePayload writes the unique payload for index i into p (len 64):
// the index lives in the first 8 bytes; the rest is deterministic filler
// so the reused buffer never carries stale bytes.
func scalePayload(p []byte, i int) {
	binary.BigEndian.PutUint64(p[:8], uint64(i))
	for j := 8; j < len(p); j++ {
		p[j] = byte(i*(j+1) + j)
	}
}

// scaleFill prefills raw with n unique objects and returns their hashes.
func scaleFill(b *testing.B, ctx context.Context, raw RawStore, n int) []Hash {
	b.Helper()
	hs := make([]Hash, n)
	p := make([]byte, scaleObjSize)
	for i := 0; i < n; i++ {
		scalePayload(p, i)
		h, err := HashBytes("sha256", p)
		if err != nil {
			b.Fatal(err)
		}
		hs[i] = h
		if err := raw.Put(ctx, h, bytes.NewReader(p)); err != nil {
			b.Fatal(err)
		}
	}
	return hs
}

// scaleReport prints, once per benchmark run, the measured rate and the
// extrapolation to scaleTarget objects. For the FS backend it also reports
// file bytes per object (Stats counts object-file bytes only — directory
// entries, fan-out dirs and inode overhead are on top of that).
func scaleReport(b *testing.B, op string, n int, raw RawStore) {
	rate := float64(b.N) / b.Elapsed().Seconds()
	hours := float64(scaleTarget) / rate / 3600
	line := fmt.Sprintf("[scale] %s @ %d objects: %.0f obj/s -> 10^10 objects ~ %.1f h",
		op, n, rate, hours)
	if fs, ok := raw.(*FSRawStore); ok {
		if st, err := fs.Stats(context.Background()); err == nil && st.ObjectCount > 0 {
			per := float64(st.TotalSize) / float64(st.ObjectCount)
			line += fmt.Sprintf(" | %.2f B/obj file bytes -> %.1f GiB for 10^10", per,
				per*float64(scaleTarget)/(1024*1024*1024))
		}
	}
	b.Logf("%s", line)
}

// BenchmarkScalePut measures appending new unique objects to a store that
// already holds CASK_SCALE_OBJECTS objects: the per-Put cost at scale.
func BenchmarkScalePut(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			ctx := context.Background()
			raw := be.new(b)
			scaleFill(b, ctx, raw, n) // setup, not timed
			p := make([]byte, scaleObjSize)
			b.SetBytes(scaleObjSize)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scalePayload(p, n+i)
				h, err := HashBytes("sha256", p)
				if err != nil {
					b.Fatal(err)
				}
				if err := raw.Put(ctx, h, bytes.NewReader(p)); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Put", n, raw)
		})
	}
}

// BenchmarkScaleGet measures reads of existing objects in a store holding
// CASK_SCALE_OBJECTS objects.
func BenchmarkScaleGet(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			ctx := context.Background()
			raw := be.new(b)
			hs := scaleFill(b, ctx, raw, n)
			b.SetBytes(scaleObjSize)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := raw.Get(ctx, hs[i%n])
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, r); err != nil {
					b.Fatal(err)
				}
				if err := r.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Get", n, raw)
		})
	}
}

// BenchmarkScaleExists measures existence checks in a store holding
// CASK_SCALE_OBJECTS objects.
func BenchmarkScaleExists(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			ctx := context.Background()
			raw := be.new(b)
			hs := scaleFill(b, ctx, raw, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := raw.Exists(ctx, hs[i%n]); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Exists", n, raw)
		})
	}
}

// BenchmarkScaleDelete measures deletions from a store holding
// CASK_SCALE_OBJECTS objects (the store shrinks as the loop runs).
func BenchmarkScaleDelete(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			ctx := context.Background()
			raw := be.new(b)
			hs := scaleFill(b, ctx, raw, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := raw.Delete(ctx, hs[i%n]); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Delete", n, raw)
		})
	}
}

// BenchmarkScaleList measures full List scans (materializes every hash) of
// a store holding CASK_SCALE_OBJECTS objects. Keep N modest: each op
// allocates the whole hash slice.
func BenchmarkScaleList(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			ctx := context.Background()
			raw := be.new(b)
			scaleFill(b, ctx, raw, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				hh, err := raw.List(ctx, "sha256")
				if err != nil {
					b.Fatal(err)
				}
				if len(hh) == 0 {
					b.Fatal("List returned nothing")
				}
			}
			b.StopTimer()
			scaleReport(b, "List", n, raw)
		})
	}
}

// BenchmarkScaleStats measures StoreStats at CASK_SCALE_OBJECTS objects
// (FSRawStore only — MemoryRawStore has no Stats).
func BenchmarkScaleStats(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			ctx := context.Background()
			raw := be.new(b)
			fs, ok := raw.(*FSRawStore)
			if !ok {
				b.Skip("backend has no Stats")
			}
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			scaleFill(b, ctx, raw, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fs.Stats(ctx); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Stats", n, raw)
		})
	}
}
