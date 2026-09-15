package persistent

import (
	"fmt"
	"os"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// Filter is a file-backed Bloom filter intended for large stores that need to
// survive restarts without rebuilding the filter.
type Filter struct {
	file   *os.File
	data   []byte
	k      int
	m      uint64
	hash   bloom.IndexHash
	mu     sync.RWMutex
	mapped bool
	path   string
}

// Config configures a persistent Bloom filter.
type Config struct {
	ExpectedItems     uint64
	FalsePositiveRate float64
	Hash              bloom.IndexHash
}

// New creates a persistent Bloom filter at path.
func New(path string, expectedItems uint64, falsePositiveRate float64) (*Filter, error) {
	return NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: falsePositiveRate}, path)
}

// NewFilter creates a persistent Bloom filter from a config and path.
func NewFilter(cfg Config, path string) (*Filter, error) {
	if cfg.ExpectedItems == 0 {
		return nil, fmt.Errorf("bloom/persistent: expected items must be > 0")
	}
	if err := bloom.ValidateFalsePositiveRate(cfg.FalsePositiveRate, "bloom/persistent"); err != nil {
		return nil, err
	}
	m, k := bloom.Parameters(cfg.ExpectedItems, cfg.FalsePositiveRate)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("bloom/persistent: open persistent file: %w", err)
	}
	bytesLen := int((m + 7) / 8)
	if fi, err := file.Stat(); err == nil && fi.Size() == 0 {
		if err := file.Truncate(int64(bytesLen)); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("bloom/persistent: resize persistent file: %w", err)
		}
	}
	if fi, err := file.Stat(); err == nil && fi.Size() < int64(bytesLen) {
		if err := file.Truncate(int64(bytesLen)); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("bloom/persistent: expand persistent file: %w", err)
		}
	}

	mapped, data, err := mmapBytes(file, bytesLen)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Filter{file: file, data: data, k: k, m: m, hash: bloom.ResolveIndexHash(cfg.Hash), mapped: mapped, path: path}, nil
}

// Add records a digest in the persistent bloom filter.
func (f *Filter) Add(d cas.Digest) {
	if d.IsZero() {
		return
	}
	positions := bloom.Indices(f.hash, d.Bytes(), f.k, f.m)
	for _, idx := range positions {
		f.mu.Lock()
		f.data[idx/8] |= 1 << (idx % 8)
		f.mu.Unlock()
	}
}

// Contains reports whether d may be present.
func (f *Filter) Contains(d cas.Digest) bool {
	if d.IsZero() {
		return false
	}
	positions := bloom.Indices(f.hash, d.Bytes(), f.k, f.m)
	for _, idx := range positions {
		f.mu.RLock()
		bit := f.data[idx/8]&(1<<(idx%8)) != 0
		f.mu.RUnlock()
		if !bit {
			return false
		}
	}
	return true
}

// Reset clears all bits.
func (f *Filter) Reset() {
	f.mu.Lock()
	for i := range f.data {
		f.data[i] = 0
	}
	f.mu.Unlock()
}

// Close releases the backing store.
func (f *Filter) Close() error {
	if f == nil {
		return nil
	}
	if !f.mapped {
		if err := f.Sync(); err != nil {
			_ = f.file.Close()
			return err
		}
	}
	if f.mapped {
		if err := closeMapped(f.data); err != nil {
			_ = f.file.Close()
			return err
		}
	}
	return f.file.Close()
}

// Sync flushes the persistent state to disk.
func (f *Filter) Sync() error {
	if f == nil || f.file == nil {
		return nil
	}
	if f.mapped {
		return f.file.Sync()
	}
	if len(f.data) == 0 {
		return nil
	}
	return os.WriteFile(f.path, f.data, 0o644)
}

