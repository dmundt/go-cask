package standard

import (
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// Filter is a standard in-memory Bloom filter keyed by cas.Digest bytes.
type Filter struct {
	bits []uint64
	k    int
	m    uint64
	hash bloom.IndexHash
	mu   sync.RWMutex
}

// Config is the expected size and false-positive target for a new filter.
type Config struct {
	ExpectedItems     uint64
	FalsePositiveRate float64
	Hash              bloom.IndexHash
}

// New creates a Bloom filter sized to hold the expected number of items.
func New(expectedItems uint64, falsePositiveRate float64) (*Filter, error) {
	return NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: falsePositiveRate})
}

// NewFilter creates a Bloom filter with the supplied config.
func NewFilter(cfg Config) (*Filter, error) {
	if cfg.ExpectedItems == 0 {
		return nil, fmt.Errorf("bloom/standard: expected items must be > 0")
	}
	if err := bloom.ValidateFalsePositiveRate(cfg.FalsePositiveRate, "bloom/standard"); err != nil {
		return nil, err
	}
	m, k := bloom.Parameters(cfg.ExpectedItems, cfg.FalsePositiveRate)
	bits := make([]uint64, (m+63)/64)
	return &Filter{bits: bits, k: k, m: m, hash: bloom.ResolveIndexHash(cfg.Hash)}, nil
}

// Add records d as present in the Bloom filter.
func (f *Filter) Add(d cas.Digest) {
	if d.IsZero() {
		return
	}
	data := d.Bytes()
	hash := f.hash
	bits := f.bits
	m := f.m
	k := f.k

	f.mu.Lock()
	for i := 0; i < k; i++ {
		idx := hash(data, i) % m
		bits[idx>>6] |= uint64(1) << (idx & 63)
	}
	f.mu.Unlock()
}

// Contains reports whether d may exist in the filter.
func (f *Filter) Contains(d cas.Digest) bool {
	if d.IsZero() {
		return false
	}
	data := d.Bytes()
	hash := f.hash
	bits := f.bits
	m := f.m
	k := f.k

	f.mu.RLock()
	defer f.mu.RUnlock()
	for i := 0; i < k; i++ {
		idx := hash(data, i) % m
		if bits[idx>>6]&(uint64(1)<<(idx&63)) == 0 {
			return false
		}
	}
	return true
}

// Reset clears every bit in the filter.
func (f *Filter) Reset() {
	f.mu.Lock()
	for i := range f.bits {
		f.bits[i] = 0
	}
	f.mu.Unlock()
}
