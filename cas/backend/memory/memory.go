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
)

// Backend is an in-memory Backend keeping objects in a map[string][]byte,
// guarded by an RWMutex.
type Backend struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// New creates an empty in-memory backend.
func New() *Backend {
	return &Backend{objects: make(map[string][]byte)}
}

// Put buffers r and stores it under h. Idempotent.
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
	stored := make([]byte, len(data))
	copy(stored, data)
	m.objects[h.String()] = stored
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
	delete(m.objects, h.String())
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
