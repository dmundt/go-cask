package persistent

import (
	"fmt"
	"os"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/bloom"
)

// Filter is a Bloom filter whose bitset is stored in a single file so the hint
// set survives process restarts without a rebuild.
//
// The file is a header followed by the bitset (see header.go). The header carries
// the index key the default index hash is derived from, so a filter reopened by
// the next process indexes the same bits as the one that wrote them: a Bloom
// index built from a process-local seed would report false for every recorded
// digest there, and bloom.Guard turns a negative into an authoritative absence
// (#254). A caller-supplied Config.Hash must be deterministic for the same
// reason; the header records which kind wrote the file, and a file written under
// the other kind is rebuilt rather than trusted.
//
// The backing strategy is platform dependent and reported by IsMapped: when the
// platform can memory map the file, the bitset is a live shared view of it;
// otherwise the bitset is a heap buffer that Sync and Close write back with
// os.WriteFile. Windows always uses the heap path today, so a Windows filter is
// durable but never memory mapped. Either way the filter stays advisory: the
// backend and the caller-owned CAS hasher remain the authority for object
// identity and existence.
//
// A Filter is safe for concurrent use while it is open. It MUST NOT be used
// after Close: Add and Reset become no-ops and Contains reports false, so a
// closed filter never touches released memory. Close itself is idempotent and
// may be called again at any time.
type Filter struct {
	mu     sync.RWMutex
	file   *os.File
	raw    []byte
	data   []byte
	k      int
	m      uint64
	hash   bloom.IndexHash
	mapped bool
	closed bool
	path   string
	driver mmapDriver
}

// Config configures a persistent Bloom filter.
type Config struct {
	// ExpectedItems is the anticipated number of distinct digests.
	ExpectedItems uint64
	// FalsePositiveRate is the target false-positive probability.
	FalsePositiveRate float64
	// Hash optionally selects the digest indexing function.
	Hash bloom.IndexHash
}

// New creates a persistent Bloom filter at path.
func New(path string, expectedItems uint64, falsePositiveRate float64) (*Filter, error) {
	return NewFilter(Config{ExpectedItems: expectedItems, FalsePositiveRate: falsePositiveRate}, path)
}

// NewFilter creates a persistent Bloom filter from a config and path.
//
// An existing file whose header names the same index-hash kind is reused: its
// bits are preserved, and it is grown with Truncate when it is shorter than the
// configured size. A file with no usable header — one written before the header
// existed, a truncated one, or one written under the other index-hash kind — is
// rebuilt empty with a fresh index key, because its bits cannot be indexed the
// way this filter reads them. The hint set is a cache, so an unusable file costs
// a rebuild rather than a wrong answer; callers that want a clean filter for the
// same shape must remove the file first or call Reset.
func NewFilter(cfg Config, path string) (*Filter, error) {
	return newFilter(cfg, path, defaultMmapDriver())
}

// newFilter is the constructor NewFilter delegates to. The driver parameter is
// the supported injection point for tests that need to simulate a mapping
// failure or a platform without mmap; production code always passes
// defaultMmapDriver.
func newFilter(cfg Config, path string, driver mmapDriver) (*Filter, error) {
	if cfg.ExpectedItems == 0 {
		return nil, fmt.Errorf("bloom/persistent: expected items must be > 0")
	}
	if err := bloom.ValidateFalsePositiveRate(cfg.FalsePositiveRate, "bloom/persistent"); err != nil {
		return nil, err
	}
	m, k, err := bloom.Parameters(cfg.ExpectedItems, cfg.FalsePositiveRate)
	if err != nil {
		return nil, fmt.Errorf("bloom/persistent: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("bloom/persistent: open persistent file: %w", err)
	}
	bytesLen := (m + 7) / 8
	// bloom.Parameters bounds m by bloom.MaxBits, so the bitset and its header
	// always fit an int and no overflow check is needed here.
	total := headerSize + int(bytesLen)
	fi, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("bloom/persistent: stat persistent file: %w", err)
	}
	if fi.Size() < int64(total) {
		if err := file.Truncate(int64(total)); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("bloom/persistent: resize persistent file: %w", err)
		}
	}

	mapped, raw, err := driver.mapFile(file, total)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("bloom/persistent: map persistent file: %w", err)
	}
	f := &Filter{
		file:   file,
		raw:    raw,
		data:   raw[headerSize:],
		k:      k,
		m:      m,
		mapped: mapped,
		path:   path,
		driver: driver,
	}
	if err := f.initIndexHash(cfg.Hash); err != nil {
		_ = f.closeLocked()
		return nil, err
	}
	return f, nil
}

// initIndexHash resolves the filter's index hash and makes the header describe
// it. A header that is missing, unreadable, or names the other index-hash kind
// makes the stored bits unusable under this filter's indexing, so the file is
// rebuilt empty with a fresh key: the hint set is a cache, and rebuilding it is
// always safer than answering from bits indexed another way (#254).
func (f *Filter) initIndexHash(custom bloom.IndexHash) error {
	kind := hashKindDefault
	if custom != nil {
		kind = hashKindCustom
	}
	gotKind, key, ok := decodeHeader(f.raw[:headerSize])
	if !ok || gotKind != kind {
		fresh, err := randomKey()
		if err != nil {
			return err
		}
		key = fresh
		encodeHeader(f.raw[:headerSize], kind, key)
		clear(f.data)
	}
	if custom != nil {
		f.hash = custom
		return nil
	}
	f.hash = keyedIndexHash(key)
	return nil
}

// IsMapped reports whether the bitset is a live memory-mapped view of the
// backing file rather than a heap buffer. It reports false on Windows, and false
// once the filter has been closed.
func (f *Filter) IsMapped() bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mapped && !f.closed
}

// Add records a digest in the persistent bloom filter. It is a no-op on a nil
// filter, for the zero digest, and after Close.
func (f *Filter) Add(d cas.Digest) {
	if f == nil || d.IsZero() {
		return
	}
	data := d.Bytes()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	bits := f.data
	hash := f.hash
	m := f.m
	k := f.k
	for i := range k {
		idx := hash(data, i) % m
		bits[idx>>3] |= byte(1) << (idx & 7)
	}
}

// Contains reports whether d may be present. It reports false on a nil filter,
// for the zero digest, and after Close.
func (f *Filter) Contains(d cas.Digest) bool {
	if f == nil || d.IsZero() {
		return false
	}
	data := d.Bytes()

	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return false
	}
	bits := f.data
	hash := f.hash
	m := f.m
	k := f.k
	for i := range k {
		idx := hash(data, i) % m
		if bits[idx>>3]&(byte(1)<<(idx&7)) == 0 {
			return false
		}
	}
	return true
}

// Reset clears all bits. It is a no-op on a nil filter and after Close.
func (f *Filter) Reset() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	for i := range f.data {
		f.data[i] = 0
	}
}

// Close flushes and releases the backing store.
//
// Close is idempotent: a second call, a call on a nil *Filter, or a call on a
// filter whose first Close already failed all return nil. After the first Close
// the filter MUST NOT be used again; see Filter's documentation for the exact
// behavior of the remaining methods.
func (f *Filter) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeLocked()
}

// Sync flushes the bitset to the backing file. It is a no-op on a nil filter and
// after Close.
func (f *Filter) Sync() error {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return nil
	}
	return f.syncLocked()
}

// closeLocked marks the filter closed and releases everything it owns. The
// caller must hold f.mu for writing.
func (f *Filter) closeLocked() error {
	if f.closed {
		return nil
	}
	f.closed = true

	var firstErr error
	if err := f.syncLocked(); err != nil {
		firstErr = err
	}
	if f.mapped && len(f.raw) > 0 {
		// The mapping covers the header as well as the bitset, so it must be
		// released with the bounds it was created with.
		if err := f.driver.unmap(f.raw); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	f.raw = nil
	f.data = nil
	if f.file != nil {
		if err := f.file.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("bloom/persistent: close persistent file: %w", err)
		}
		f.file = nil
	}
	return firstErr
}

// syncLocked flushes the header and bitset to the backing store. The caller must
// hold f.mu.
func (f *Filter) syncLocked() error {
	if len(f.raw) == 0 {
		return nil
	}
	if f.mapped {
		if err := f.driver.flush(f.raw); err != nil {
			return err
		}
		if f.file != nil {
			if err := f.file.Sync(); err != nil {
				return fmt.Errorf("bloom/persistent: sync persistent file: %w", err)
			}
		}
		return nil
	}
	// The heap fallback owns the whole file, header included, so it is written in
	// one call: that keeps the header and the bits it describes inseparable.
	if err := os.WriteFile(f.path, f.raw, 0o644); err != nil {
		return fmt.Errorf("bloom/persistent: write persistent file: %w", err)
	}
	return nil
}
