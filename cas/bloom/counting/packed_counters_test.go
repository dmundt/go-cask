package counting

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// TestFilterCounterArrayIsPackedToTheConfiguredWidth pins the memory shape
// go-cask#383 was filed about: CounterBits selects the slot width, so the backing
// array is ceil(m*bits/8) bytes — two 4-bit counters per byte, one 8-bit counter
// per byte, one little-endian 16-bit counter per two bytes — instead of the four
// bytes per bit a []uint32 slot array cost. Each width must therefore cost more
// than the one below it, not exactly the same.
func TestFilterCounterArrayIsPackedToTheConfiguredWidth(t *testing.T) {
	const expectedItems = 4096
	m, _, err := bloom.Parameters(expectedItems, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	sizes := make(map[int]int, 3)
	for _, bits := range []int{4, 8, 16} {
		f, err := New(expectedItems, 0.01, bits)
		if err != nil {
			t.Fatalf("New(%d items, 0.01, %d bits) = %v", expectedItems, bits, err)
		}
		want := int((m*uint64(bits) + 7) / 8)
		if len(f.counts) != want || cap(f.counts) != want {
			t.Fatalf("%d-bit counter array = len %d cap %d, want %d bytes for %d counters at %d bits per slot",
				bits, len(f.counts), cap(f.counts), want, m, bits)
		}
		if f.bits != bits {
			t.Fatalf("filter slot width = %d, want the configured %d", f.bits, bits)
		}
		if want := uint32((1 << bits) - 1); f.mask != want {
			t.Fatalf("%d-bit saturation mask = %d, want %d", bits, f.mask, want)
		}
		if uint64(len(f.counts)) >= m*4 {
			t.Fatalf("%d-bit counter array is %d bytes, want well below the %d bytes a 32-bit slot array cost",
				bits, len(f.counts), m*4)
		}
		sizes[bits] = len(f.counts)
	}
	if !(sizes[4] < sizes[8] && sizes[8] < sizes[16]) {
		t.Fatalf("counter array sizes = %v, want each width to cost more than the one below it", sizes)
	}
}

// TestPackedCounterBytesReachesTheDocumentedCeiling pins the arithmetic the 2 GiB
// figure rests on: at bloom.MaxBits bits a 4-bit filter fills MaxCounterBytes
// exactly, and the wider counters exceed it, which is why NewFilter refuses them
// below the package-wide bit ceiling. A shape that lands exactly on the budget is
// accepted — the check is `>` — so the boundary is stated here rather than
// discovered by an allocation.
func TestPackedCounterBytesReachesTheDocumentedCeiling(t *testing.T) {
	if got := packedCounterBytes(bloom.MaxBits, 4); got != MaxCounterBytes {
		t.Fatalf("4-bit counters at bloom.MaxBits = %d bytes, want the %d-byte budget", got, MaxCounterBytes)
	}
	if MaxCounterBytes != 1<<31 {
		t.Fatalf("MaxCounterBytes = %d, want %d (2 GiB)", MaxCounterBytes, uint64(1)<<31)
	}
	for _, bits := range []int{8, 16} {
		if got := packedCounterBytes(bloom.MaxBits, bits); got <= MaxCounterBytes {
			t.Fatalf("%d-bit counters at bloom.MaxBits = %d bytes, want more than the %d-byte budget", bits, got, MaxCounterBytes)
		}
	}
	if got := packedCounterBytes(1<<30, 16); got != MaxCounterBytes {
		t.Fatalf("16-bit counters over %d counters = %d bytes, want the %d-byte budget exactly", uint64(1)<<30, got, MaxCounterBytes)
	}
}

// TestNewFilterRefusesAShapeAboveTheCounterBudget pins the fail-fast half of
// go-cask#383: a configuration whose packed counters would exceed
// MaxCounterBytes is reported as ErrFilterTooLarge before anything is allocated,
// so a mis-sized filter is an error a caller can act on rather than an
// out-of-memory failure. The shape is chosen so bloom.Parameters accepts its
// computed bit count and only the counter budget rejects it.
func TestNewFilterRefusesAShapeAboveTheCounterBudget(t *testing.T) {
	const expectedItems = 200_000_000
	f, err := NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: 0.01, CounterBits: 16})
	if err == nil {
		t.Fatalf("NewFilter(%d items, 16-bit counters) = %v, want the counter-budget error", expectedItems, f)
	}
	if f != nil {
		t.Fatal("a refused configuration still returned a filter")
	}
	if !errors.Is(err, ErrFilterTooLarge) {
		t.Fatalf("NewFilter = %v, want errors.Is(err, ErrFilterTooLarge)", err)
	}
	if !strings.Contains(err.Error(), "bloom/counting") {
		t.Fatalf("NewFilter = %v, want it to name the package", err)
	}
	if want := fmt.Sprintf("%d-byte counter budget", MaxCounterBytes); !strings.Contains(err.Error(), want) {
		t.Fatalf("NewFilter = %v, want it to name the %s", err, want)
	}
}

// TestFilterPackedCountersKeepTheirNeighboursIntact pins the byte-level layout
// the packing relies on: two 4-bit counters share one byte and neither write
// disturbs the other, and a 16-bit counter spans two bytes little-endian without
// spilling into the slot beside it.
func TestFilterPackedCountersKeepTheirNeighboursIntact(t *testing.T) {
	four := &Filter{counts: make([]uint8, 2), bits: 4}
	four.setCounter(0, 15)
	four.setCounter(1, 5)
	if four.counts[0] != 0x5f {
		t.Fatalf("4-bit byte = %#x, want 0x5f (slot 0 in the low nibble, slot 1 in the high)", four.counts[0])
	}
	if got := four.counter(0); got != 15 {
		t.Fatalf("4-bit slot 0 = %d, want 15", got)
	}
	if got := four.counter(1); got != 5 {
		t.Fatalf("4-bit slot 1 = %d, want 5", got)
	}
	four.setCounter(0, 0)
	if four.counts[0] != 0x50 {
		t.Fatalf("clearing slot 0 left %#x, want 0x50: the counter sharing its byte changed with it", four.counts[0])
	}

	sixteen := &Filter{counts: make([]uint8, 6), bits: 16}
	sixteen.setCounter(1, 0x1234)
	if sixteen.counts[2] != 0x34 || sixteen.counts[3] != 0x12 {
		t.Fatalf("16-bit slot 1 bytes = %#x %#x, want 34 12 (little-endian)", sixteen.counts[2], sixteen.counts[3])
	}
	if got := sixteen.counter(1); got != 0x1234 {
		t.Fatalf("16-bit slot 1 = %#x, want 0x1234", got)
	}
	if sixteen.counter(0) != 0 || sixteen.counter(2) != 0 {
		t.Fatal("a 16-bit write spilled into a neighbouring slot")
	}
}

// TestFilterCountersSaturateAtTheConfiguredWidth drives Add past every width's
// maximum and pins that each probe slot stops at the width's mask instead of
// wrapping into the neighbour sharing its byte. Remove walks the same slots back
// down, once per probe that reached each slot, and a digest whose slots are all
// non-zero still reports present.
func TestFilterCountersSaturateAtTheConfiguredWidth(t *testing.T) {
	for _, bits := range []int{4, 8, 16} {
		t.Run(fmt.Sprintf("%d-bit", bits), func(t *testing.T) {
			f, err := New(64, 0.01, bits)
			if err != nil {
				t.Fatal(err)
			}
			d := cas.NewDigest([]byte("saturating digest"))
			mask := uint32((1 << bits) - 1)

			// One Add past the width's maximum: every probe slot is saturated,
			// and a further Add is what the saturation guard has to absorb.
			for range int(mask) + 1 {
				f.Add(d)
			}
			probes := make(map[uint64]int, f.k)
			for i := range f.k {
				probes[f.hash([]byte(d), i)%f.m]++
			}
			for idx := range probes {
				if got := f.counter(idx); got != mask {
					t.Fatalf("slot %d after %d Adds = %d, want the %d-bit maximum %d", idx, uint32(mask)+1, got, bits, mask)
				}
			}

			f.Remove(d)
			for idx, hits := range probes {
				if want := mask - uint32(hits); f.counter(idx) != want {
					t.Fatalf("slot %d after one Remove = %d, want %d (one decrement per probe that reached it)",
						idx, f.counter(idx), want)
				}
			}
			if !f.Contains(d) {
				t.Fatal("a digest whose every probe slot is above zero reported absent")
			}

			// A digest nothing added leaves the slots it probes at zero, and
			// Remove must stop there rather than underflowing a counter.
			f.Remove(cas.NewDigest([]byte("never added")))
			for idx := range f.m {
				if got := f.counter(idx); got > mask {
					t.Fatalf("slot %d = %d after Remove, above the %d-bit maximum %d: a counter underflowed", idx, got, bits, mask)
				}
			}
		})
	}
}
