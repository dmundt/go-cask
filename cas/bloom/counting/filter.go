package counting

import (
	"errors"
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// ErrFilterTooLarge reports a configuration whose packed counters would need more
// than MaxCounterBytes. It is returned instead of attempting the allocation,
// because a `make` that large is an out-of-memory failure or a panic rather than
// the error a caller can act on (#383). Callers test it with errors.Is.
var ErrFilterTooLarge = errors.New("bloom/counting: filter exceeds the counter budget")

// MaxCounterBytes is the largest counter array NewFilter allocates: 2 GiB, half
// of bloom.MaxBits, and a counting filter's own ceiling below that package-wide
// bit limit. The counters are packed to Config.CounterBits, so a 4-bit filter
// reaches this budget at the full bloom.MaxBits bits, while an 8- or 16-bit
// filter reaches it at half or a quarter of those bits and NewFilter reports
// ErrFilterTooLarge above that. Shard large key spaces rather than sizing one
// filter to the limit.
const MaxCounterBytes uint64 = bloom.MaxBits / 2

// Filter is a counting Bloom filter. It supports Add, Remove and Contains and is
// suitable for reference accounting, GC candidate tracking, and cache
// invalidation.
//
// Counters are packed to the configured width — two 4-bit counters per byte, one
// 8-bit counter per byte, or one little-endian 16-bit counter per two bytes — so
// the backing array is m*CounterBits/8 bytes rather than one machine word per
// bit. A slot saturates at its width's maximum and never wraps, so a Remove that
// is not balanced by an Add under-counts rather than corrupting a neighbour.
type Filter struct {
	counts []uint8
	k      int
	m      uint64
	bits   int
	mask   uint32
	hash   bloom.IndexHash
	mu     sync.RWMutex
}

// Config configures a counting Bloom filter.
type Config struct {
	// ExpectedItems is the anticipated number of distinct digests.
	ExpectedItems uint64
	// FalsePositiveRate is the target false-positive probability.
	FalsePositiveRate float64
	// CounterBits selects the counter width and so the memory a slot costs: 4,
	// 8, or 16 bits.
	CounterBits int
	// Hash optionally selects the digest indexing function.
	Hash bloom.IndexHash
}

// New creates a 4/8/16-bit counting Bloom filter.
func New(expectedItems uint64, falsePositiveRate float64, counterBits int) (*Filter, error) {
	return NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: falsePositiveRate, CounterBits: counterBits})
}

// NewFilter creates a counting filter from a config. The counters are packed to
// Config.CounterBits, so the backing array is m*CounterBits/8 bytes; a shape
// whose packed counters would exceed MaxCounterBytes is reported as
// ErrFilterTooLarge before it is allocated.
func NewFilter(cfg Config) (*Filter, error) {
	if cfg.ExpectedItems == 0 {
		return nil, fmt.Errorf("bloom/counting: expected items must be > 0")
	}
	if err := bloom.ValidateFalsePositiveRate(cfg.FalsePositiveRate, "bloom/counting"); err != nil {
		return nil, err
	}
	if cfg.CounterBits != 4 && cfg.CounterBits != 8 && cfg.CounterBits != 16 {
		return nil, fmt.Errorf("bloom/counting: counter bits must be one of 4, 8, 16")
	}
	m, k, err := bloom.Parameters(cfg.ExpectedItems, cfg.FalsePositiveRate)
	if err != nil {
		return nil, fmt.Errorf("bloom/counting: %w", err)
	}
	size := packedCounterBytes(m, cfg.CounterBits)
	if size > MaxCounterBytes {
		return nil, fmt.Errorf("bloom/counting: expected items %d at false positive rate %v needs %d counters packed at %d bits, %d bytes above the %d-byte counter budget; shard the key space, use a coarser rate, or use narrower counters: %w",
			cfg.ExpectedItems, cfg.FalsePositiveRate, m, cfg.CounterBits, size, MaxCounterBytes, ErrFilterTooLarge)
	}
	return &Filter{
		counts: make([]uint8, size),
		k:      k,
		m:      m,
		bits:   cfg.CounterBits,
		mask:   uint32((1 << cfg.CounterBits) - 1),
		hash:   bloom.ResolveIndexHash(cfg.Hash),
	}, nil
}

// packedCounterBytes returns the number of bytes m counters need when each is
// counterBits wide. m is bounded by bloom.MaxBits and counterBits by 16, so the
// product cannot overflow a uint64.
func packedCounterBytes(m uint64, counterBits int) uint64 {
	return (m*uint64(counterBits) + 7) / 8
}

// counter reads the counter in slot idx. idx is a slot index, not a bit offset:
// the counters are packed, so a 4-bit filter holds two per byte and a 16-bit
// filter holds one per two bytes.
func (f *Filter) counter(idx uint64) uint32 {
	switch f.bits {
	case 4:
		b := f.counts[idx>>1]
		if idx&1 == 0 {
			return uint32(b & 0x0f)
		}
		return uint32(b >> 4)
	case 8:
		return uint32(f.counts[idx])
	default: // 16
		pos := idx << 1
		return uint32(f.counts[pos]) | uint32(f.counts[pos+1])<<8
	}
}

// setCounter stores v in slot idx without disturbing the counters that share its
// byte. v is never above the width's mask: Add saturates at it.
func (f *Filter) setCounter(idx uint64, v uint32) {
	switch f.bits {
	case 4:
		pos := idx >> 1
		if idx&1 == 0 {
			f.counts[pos] = f.counts[pos]&0xf0 | uint8(v)
			return
		}
		f.counts[pos] = f.counts[pos]&0x0f | uint8(v)<<4
	case 8:
		f.counts[idx] = uint8(v)
	default: // 16
		pos := idx << 1
		f.counts[pos] = uint8(v)
		f.counts[pos+1] = uint8(v >> 8)
	}
}

// Add increments each counter for the digest, saturating at the counter width.
func (f *Filter) Add(d cas.Digest) {
	if d.IsZero() {
		return
	}
	// A view of the digest's own bytes, not a copy: a Digest is immutable
	// (cas/digest.go) and the filter only reads this slice inside the probe
	// loop, so d.Bytes() would allocate a fresh copy per operation on the hot
	// path the filter exists to make cheap.
	data := []byte(d)
	hash := f.hash
	mask := f.mask
	m := f.m
	k := f.k

	f.mu.Lock()
	for i := range k {
		idx := hash(data, i) % m
		if v := f.counter(idx); v < mask {
			f.setCounter(idx, v+1)
		}
	}
	f.mu.Unlock()
}

// Remove decrements each counter for the digest, stopping at zero.
func (f *Filter) Remove(d cas.Digest) {
	if d.IsZero() {
		return
	}
	data := []byte(d) // a read-only view; see Add
	hash := f.hash
	m := f.m
	k := f.k

	f.mu.Lock()
	for i := range k {
		idx := hash(data, i) % m
		if v := f.counter(idx); v > 0 {
			f.setCounter(idx, v-1)
		}
	}
	f.mu.Unlock()
}

// Contains reports whether the digest is possibly present.
func (f *Filter) Contains(d cas.Digest) bool {
	if d.IsZero() {
		return false
	}
	data := []byte(d) // a read-only view; see Add
	hash := f.hash
	m := f.m
	k := f.k

	f.mu.RLock()
	defer f.mu.RUnlock()
	for i := range k {
		idx := hash(data, i) % m
		if f.counter(idx) == 0 {
			return false
		}
	}
	return true
}

// Reset clears all counts.
func (f *Filter) Reset() {
	f.mu.Lock()
	clear(f.counts)
	f.mu.Unlock()
}
