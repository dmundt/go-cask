package cas

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// MemoryRawStore is an in-memory RawStore backend keeping objects in a
// map[string][]byte keyed by h.String(), guarded by an RWMutex. It is fast,
// dependency-free and deterministic — intended for unit, property and fuzz
// tests and for benchmarks that isolate store logic from disk noise. It is
// NOT persistent.
//
// Contracts match FSRawStore: idempotent Put (same hash ⇒ identical bytes),
// Get returns a reader the caller MUST close (missing → ErrNotFound),
// Delete is a no-op on missing objects, List(algo) filters by algorithm.
// Put buffers the whole stream (io.ReadAll); Get returns a NopCloser over
// the stored slice, which is never mutated after Put.
type MemoryRawStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewMemoryRawStore creates an empty in-memory backend. Swap-in compatible
// with any Store[T], gitlike repository, or handler that takes a RawStore.
func NewMemoryRawStore() *MemoryRawStore {
	return &MemoryRawStore{objects: make(map[string][]byte)}
}

// Put buffers r and stores it under h. Idempotent: a repeated Put of the
// same hash replaces the entry with identical bytes.
func (m *MemoryRawStore) Put(ctx context.Context, h Hash, r io.Reader) error {
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

// Get returns a reader over the stored bytes; the caller MUST close it. A
// missing object returns ErrNotFound. The returned slice is never mutated
// after Put, so no copy is made on read.
func (m *MemoryRawStore) Get(ctx context.Context, h Hash) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.objects[h.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, h)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Exists reports whether the object is stored.
func (m *MemoryRawStore) Exists(ctx context.Context, h Hash) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.objects[h.String()]
	return ok, nil
}

// Delete removes the object. A missing object is a no-op (no error).
func (m *MemoryRawStore) Delete(ctx context.Context, h Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, h.String())
	return nil
}

// List returns every stored hash, filtered by algorithm when algo != "".
func (m *MemoryRawStore) List(ctx context.Context, algo string) ([]Hash, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	hashes := make([]Hash, 0, len(m.objects))
	for key := range m.objects {
		h, err := ParseHash(key)
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
