package benchmark_test

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
//	CASK_SCALE_OBJECTS=100000  go test ./benchmarks/ -bench=Scale -run=^$ -benchtime=1000x -v
//	CASK_SCALE_OBJECTS=1000000 go test ./benchmarks/ -bench=Scale -run=^$ -benchtime=100x -v   # FS: ~GBs of temp files
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
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
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

// scaleBackend pairs a name with a Backend constructor.
type scaleBackend struct {
	name string
	new  func(tb testing.TB) cas.Backend
}

func scaleBackends() []scaleBackend {
	return []scaleBackend{
		{"Memory", func(tb testing.TB) cas.Backend { return mem.New() }},
		{"FS", func(tb testing.TB) cas.Backend {
			s, err := fs.New(tb.TempDir())
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

// scaleFill prefills raw with n unique objects and returns their digests.
func scaleFill(b *testing.B, ctx context.Context, raw cas.Backend, n int) []cas.Digest {
	b.Helper()
	hs := make([]cas.Digest, n)
	p := make([]byte, scaleObjSize)
	for i := 0; i < n; i++ {
		scalePayload(p, i)
		h := sha256.Of(p)
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
func scaleReport(b *testing.B, op string, n int, raw cas.Backend) {
	rate := float64(b.N) / b.Elapsed().Seconds()
	hours := float64(scaleTarget) / rate / 3600
	line := fmt.Sprintf("[scale] %s @ %d objects: %.0f obj/s -> 10^10 objects ~ %.1f h",
		op, n, rate, hours)
	if fsBackend, ok := raw.(*fs.Backend); ok {
		if st, err := fsBackend.Stats(context.Background()); err == nil && st.ObjectCount > 0 {
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
				h := sha256.Of(p)
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

// BenchmarkScaleList measures full List scans (materializes every digest) of
// a store holding CASK_SCALE_OBJECTS objects. Keep N modest: each op
// allocates the whole digest slice.
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
				hh, err := raw.List(ctx)
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

// BenchmarkScaleStats measures the Backend.Stats summary at CASK_SCALE_OBJECTS
// objects on every backend (Memory and FS both implement Backend.Stats).
func BenchmarkScaleStats(b *testing.B) {
	for _, be := range scaleBackends() {
		b.Run(be.name, func(b *testing.B) {
			n := scaleObjectCount(b)
			ctx := context.Background()
			raw := be.new(b)
			if b.N == 1 {
				return
			} // framework probe run (b.N=1); measure only the real run
			scaleFill(b, ctx, raw, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := raw.Stats(ctx); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			scaleReport(b, "Stats", n, raw)
		})
	}
}

// fsEconomics summarizes the on-disk cost of a loose fs store: how many
// object files, how they are spread across directories, and total bytes.
// This is the data that decides fan-out sizing (performance §8.1).
type fsEconomics struct {
	files      int
	dirs       int
	leafDirs   int
	minEntries int
	maxEntries int
	totalBytes int64
}

// measureFSEconomics walks base and counts object files and their directory
// spread. .tmp files are not present in a clean run.
func measureFSEconomics(base string) (fsEconomics, error) {
	e := fsEconomics{minEntries: -1}
	dirFiles := map[string]int{}
	err := filepath.WalkDir(base, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			e.dirs++
			return nil
		}
		e.files++
		if info, err := d.Info(); err == nil {
			e.totalBytes += info.Size()
		}
		dirFiles[filepath.Dir(p)]++
		return nil
	})
	if err != nil {
		return e, err
	}
	e.leafDirs = len(dirFiles)
	for _, c := range dirFiles {
		if e.minEntries == -1 || c < e.minEntries {
			e.minEntries = c
		}
		if c > e.maxEntries {
			e.maxEntries = c
		}
	}
	if e.leafDirs == 0 {
		e.minEntries, e.maxEntries = 0, 0
	}
	return e, nil
}

// BenchmarkScaleStoreEconomics reports the on-disk layout cost of a loose fs
// store holding CASK_SCALE_OBJECTS objects, at the default Git-like (2,1)
// layout and a wide (4,1) layout. It answers the fan-out sizing question:
// how many leaf directories exist and how evenly objects spread across them
// at this N. -v shows the [scale-econ] lines; the other scale probes give
// the per-op cost at the same N.
func BenchmarkScaleStoreEconomics(b *testing.B) {
	n := scaleObjectCount(b)
	if b.N == 1 {
		return
	}
	ctx := context.Background()
	layouts := []struct {
		name string
		new  func(base string) (*fs.Backend, error)
	}{
		{"loose-2-1", func(base string) (*fs.Backend, error) { return fs.New(base) }},
		{"loose-4-1", func(base string) (*fs.Backend, error) {
			return fs.New(base, fs.WithFanOut(4), fs.WithFanLevels(1))
		}},
	}
	for _, lay := range layouts {
		b.Run(lay.name, func(b *testing.B) {
			base := b.TempDir()
			s, err := lay.new(base)
			if err != nil {
				b.Fatal(err)
			}
			scaleFill(b, ctx, s, n)
			e, err := measureFSEconomics(base)
			if err != nil {
				b.Fatal(err)
			}
			avg := 0.0
			if e.leafDirs > 0 {
				avg = float64(e.files) / float64(e.leafDirs)
			}
			b.Logf("[scale-econ] %s @ %d objects: files=%d dirs=%d leaf_dirs=%d leaf_entries(min=%.0f avg=%.1f max=%.0f) obj_bytes=%d bytes/obj=%.1f",
				lay.name, n, e.files, e.dirs, e.leafDirs,
				float64(e.minEntries), avg, float64(e.maxEntries),
				e.totalBytes, float64(e.totalBytes)/float64(e.files))
		})
	}
}
