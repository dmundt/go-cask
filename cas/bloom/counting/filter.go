package counting

import (
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// Filter is a counting Bloom filter. It supports Add, Remove and Contains and is
// suitable for reference accounting, GC candidate tracking, and cache invalidation.
type Filter struct {
	counts []uint32
	k      int
	m      uint64
	bits   int
	mask   uint32
	hash   bloom.IndexHash
	mu     sync.RWMutex
}

// Config configures a counting Bloom filter.
type Config struct {
	ExpectedItems     uint64
	FalsePositiveRate float64
	CounterBits       int
	Hash              bloom.IndexHash
}

// New creates a 4/8/16-bit counting Bloom filter.
func New(expectedItems uint64, falsePositiveRate float64, counterBits int) (*Filter, error) {
	return NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: falsePositiveRate, CounterBits: counterBits})
}

// NewFilter creates a counting filter from a config.
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
	m, k := bloom.Parameters(cfg.ExpectedItems, cfg.FalsePositiveRate)
	mask := uint32((1 << cfg.CounterBits) - 1)
	return &Filter{counts: make([]uint32, m), k: k, m: m, bits: cfg.CounterBits, mask: mask, hash: bloom.ResolveIndexHash(cfg.Hash)}, nil
}

// Add increments each counter for the digest.
func (f *Filter) Add(d cas.Digest) {
	if d.IsZero() {
		return
	}
	positions := bloom.Indices(f.hash, d.Bytes(), f.k, f.m)
	for _, idx := range positions {
		f.mu.Lock()
		if f.counts[idx] < f.mask {
			f.counts[idx]++
		}
		f.mu.Unlock()
	}
}

// Remove decrements each counter for the digest.
func (f *Filter) Remove(d cas.Digest) {
	if d.IsZero() {
		return
	}
	positions := bloom.Indices(f.hash, d.Bytes(), f.k, f.m)
	for _, idx := range positions {
		f.mu.Lock()
		if f.counts[idx] > 0 {
			f.counts[idx]--
		}
		f.mu.Unlock()
	}
}

// Contains reports whether the digest is possibly present.
func (f *Filter) Contains(d cas.Digest) bool {
	if d.IsZero() {
		return false
	}
	positions := bloom.Indices(f.hash, d.Bytes(), f.k, f.m)
	for _, idx := range positions {
		f.mu.RLock()
		if f.counts[idx] == 0 {
			f.mu.RUnlock()
			return false
		}
		f.mu.RUnlock()
	}
	return true
}

// Reset clears all counts.
func (f *Filter) Reset() {
	f.mu.Lock()
	for i := range f.counts {
		f.counts[i] = 0
	}
	f.mu.Unlock()
}
