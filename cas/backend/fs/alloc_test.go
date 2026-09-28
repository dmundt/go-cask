package fs

import (
	"bytes"
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas/hash/sha256"
)

// putAllocBudget is the per-call allocation ceiling a steady-state Put of a
// 64-byte object must stay under. The write path legitimately allocates its
// fan-out path, temp-file name and atomic-write bookkeeping — a couple of KiB —
// but it must not allocate io.Copy's fresh 32 KiB scratch buffer, which every Put
// paid while ContextReader offered no WriterTo fast path (go-cask#368).
//
// Measured on go1.27.1, over the fixed sample below: 35 464–35 477 B/op before
// the fix, 2 608–2 878 B/op after on windows/amd64, 34 520 before and 1 752
// after on linux/amd64. The regression is a flat 32 KiB step per call, not a
// slow drift, so the ceiling separates the two by an order of magnitude.
//
// The write path's link refusal (cas-core §4.4, go-cask#352) adds two Lstats to
// every Put — the fan-out directory chain and the target entry — which measure
// 3 592–4 028 B/op on windows/amd64's more expensive stat path and 2 584–2 680
// B/op on linux/amd64. The ceiling has room for that deliberate syscall pair and
// still separates the 32 KiB regression by about 6×.
const putAllocBudget = 6 << 10

// putAllocRaceBudget is the same ceiling for a binary built with the race
// detector, which the gate's test step always is. Race instrumentation makes
// every allocation in the Put path larger, and the pooled buffer it reuses shows
// it: the same fixed sample measures 8 146–10 933 B/op with -race against
// 1 752 B/op without. The term is instrumentation, not the copy — the 32 KiB
// regression pushes the same sample past 34 KiB either way — so it gets a
// ceiling of its own instead of one ceiling loose enough for both.
const putAllocRaceBudget = 20 << 10

// putAllocRuns is the fixed sample the ceiling is measured over. The count is
// fixed rather than scaled to a wall-clock budget so the measurement is the same
// everywhere: at 200 calls a one-time 32 KiB buffer left in the pool is
// amortized to under 164 B/op, far inside either ceiling's margin, and a run is
// a few hundred small writes.
const putAllocRuns = 200

// raceDetectorEnabled reports whether this binary was built with -race, read
// from the build settings the toolchain records. It selects the ceiling; it
// never changes what is measured.
func raceDetectorEnabled() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			return true
		}
	}
	return false
}

// TestPutSmallObjectDoesNotAllocateCopyBuffer pins the allocation floor of the
// byte-layer Put: the copy of a small object into the temp file reuses a pooled
// buffer instead of allocating 32 KiB per call. The benchmark suite is manual and
// CI runs no -bench, so this test is the guard that keeps the term gone.
func TestPutSmallObjectDoesNotAllocateCopyBuffer(t *testing.T) {
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("x", 64))
	d := sha256.Of(payload)
	// Warm the path: the first Put creates the fan-out directory and fills the
	// copy pool, both setup rather than per-call cost.
	if err := s.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}

	budget := int64(putAllocBudget)
	if raceDetectorEnabled() {
		budget = putAllocRaceBudget
	}

	// The sample runs with the collector off: a GC clears sync.Pool, and every
	// clear costs a fresh 32 KiB buffer on the next call, so the collector's
	// frequency would otherwise leak into the figure. A per-call allocation,
	// which is what this guards, is unaffected by the setting.
	var before, after runtime.MemStats
	prevGC := debug.SetGCPercent(-1)
	runtime.ReadMemStats(&before)
	var putErr error
	for i := 0; i < putAllocRuns && putErr == nil; i++ {
		putErr = s.Put(ctx, d, bytes.NewReader(payload))
	}
	runtime.ReadMemStats(&after)
	debug.SetGCPercent(prevGC)
	runtime.GC() // release the sample's garbage before the rest of the package runs

	if putErr != nil {
		t.Fatalf("Put() error = %v", putErr)
	}
	perOp := int64(after.TotalAlloc-before.TotalAlloc) / putAllocRuns
	t.Logf("steady-state Put of 64 B: %d B/op over %d calls (race detector: %v, ceiling %d B/op)",
		perOp, putAllocRuns, raceDetectorEnabled(), budget)
	if perOp > budget {
		t.Fatalf("steady-state Put of 64 B allocated %d B/op, want <= %d B/op: the copy must reuse a pooled buffer, not io.Copy's 32 KiB scratch (go-cask#368)",
			perOp, budget)
	}
}
