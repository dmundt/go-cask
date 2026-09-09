// Package memory provides the in-memory Backend for the cas core — for
// tests, benchmarks and ephemeral stores. Not persistent.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
)

// config holds the in-memory backend's own configuration, applied via
// backend.Option functions.
type config struct {
	maxBytes int64
}

// WithMaxSize caps the total stored bytes. 0 (the default) means unbounded.
// Once the cap is set (> 0), every Put is checked before allocation and
// rejected with an error if it would exceed the cap.
func WithMaxSize(maxBytes int64) backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.maxBytes = maxBytes
		}
	}
}

// Backend is an in-memory Backend keeping objects in a map[string][]byte,
// guarded by an RWMutex. If configured with WithMaxSize, it tracks total
// stored bytes and rejects Puts that would exceed the cap.
type Backend struct {
	mu        sync.RWMutex
	objects   map[string][]byte
	maxBytes  int64
	usedBytes int64
}

// Compile-time check that Backend satisfies the cas.Backend interface
// (including Stats), so dropping a method breaks this package's build.
var _ cas.Backend = (*Backend)(nil)

// New creates an empty in-memory backend. Options may include WithMaxSize.
func New(opts ...backend.Option) *Backend {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}
	return &Backend{objects: make(map[string][]byte), maxBytes: cfg.maxBytes}
}

// Put buffers r and stores it under h. Idempotent. When a max size is set, a
// Put whose addition would exceed the cap is rejected with an error and no
// entry is stored.
func (m *Backend) Put(ctx context.Context, h cas.Hash, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("cas: buffer object: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := h.String()
	// Bytes this Put would add: the new blob minus any existing blob at the
	// same key (re-Put of an identical hash replaces, not grows).
	added := int64(len(data))
	if old, ok := m.objects[key]; ok {
		added -= int64(len(old))
	}
	if m.maxBytes > 0 && m.usedBytes+added > m.maxBytes {
		return fmt.Errorf("cas: memory backend would exceed max size %d bytes", m.maxBytes)
	}
	stored := make([]byte, len(data))
	copy(stored, data)
	m.objects[key] = stored
	m.usedBytes += added
	return nil
}

// Get returns a reader over the stored bytes; the caller MUST close it.
func (m *Backend) Get(ctx context.Context, h cas.Hash) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.objects[h.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, h)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Exists reports whether the object is stored.
func (m *Backend) Exists(ctx context.Context, h cas.Hash) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.objects[h.String()]
	return ok, nil
}

// Delete removes the object. A missing object is a no-op.
func (m *Backend) Delete(ctx context.Context, h cas.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := h.String()
	if old, ok := m.objects[key]; ok {
		delete(m.objects, key)
		m.usedBytes -= int64(len(old))
	}
	return nil
}

// List returns every stored hash, filtered by algorithm when algo != "".
func (m *Backend) List(ctx context.Context, algo string) ([]cas.Hash, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	hashes := make([]cas.Hash, 0, len(m.objects))
	for key := range m.objects {
		h, err := cas.ParseHash(key)
		if err != nil {
			continue
		}
		if algo != "" && h.Algorithm() != algo {
			continue
		}
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].String() < hashes[j].String() })
	return hashes, nil
}

// Stats returns per-algorithm object counts and total stored bytes.
func (m *Backend) Stats(ctx context.Context) (*cas.StoreStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := &cas.StoreStats{AlgorithmCounts: map[string]int{}}
	for key, data := range m.objects {
		h, err := cas.ParseHash(key)
		if err != nil {
			continue
		}
		st.AlgorithmCounts[h.Algorithm()]++
		st.TotalSize += int64(len(data))
		st.ObjectCount++
	}
	return st, nil
}
