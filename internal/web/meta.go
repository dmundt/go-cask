package web

import (
	"context"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/internal/index"
)

// objectMetaCacheLimit bounds the metadata cache. The entries are small and
// never invalidate, but a store larger than this would let the viewer hold a
// map proportional to it, so the cache is dropped wholesale instead of grown.
// Rebuilding costs the same reads the cache saved, which is the cheapest
// possible eviction for immutable data.
const objectMetaCacheLimit = 50_000

// objectMeta is everything the object list needs about one object. Every field
// is a property of the stored bytes, and the bytes are addressed by their own
// digest, so the values can never change for a given key — that is what makes
// caching them sound without any invalidation.
type objectMeta struct {
	// Type is the decoded envelope type, empty when the object carries none.
	Type string
	// Size is the stored payload size in bytes.
	Size int64
	// Written is the physical write time reported by the backend.
	Written time.Time
	// Unreadable reports that the bytes could not be read, which is distinct
	// from an object that is genuinely empty or untyped.
	Unreadable bool
}

// metaCache memoizes objectMeta by digest. The object list renders one row per
// stored object, and each row needs a type, a size, and a write time — without
// this cache every keystroke in the search box would re-read and re-stat the
// entire store.
type metaCache struct {
	mu       sync.Mutex
	byDigest map[string]objectMeta
}

func newMetaCache() *metaCache {
	return &metaCache{byDigest: make(map[string]objectMeta)}
}

func (c *metaCache) lookup(key string) (objectMeta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	meta, ok := c.byDigest[key]
	return meta, ok
}

func (c *metaCache) store(key string, meta objectMeta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.byDigest) >= objectMetaCacheLimit {
		clear(c.byDigest)
	}
	c.byDigest[key] = meta
}

// objectMetaFor reports the cached metadata for d, reading it once on the
// first request. A failed read is never cached: it may be transient (a
// cancelled context, a locked file), and caching it would make a recovered
// object read as unreadable for the life of the process.
func (s *Server) objectMetaFor(ctx context.Context, d cas.Digest) objectMeta {
	key := d.String()
	if meta, ok := s.meta.lookup(key); ok {
		return meta
	}
	var meta objectMeta
	data, err := s.readN(ctx, d, typePrefixLimit)
	if err != nil {
		meta.Unreadable = true
	} else {
		meta.Type = index.EnvelopeType(data)
	}
	if size, err := s.store.Size(ctx, d); err == nil {
		meta.Size = size
	} else {
		meta.Unreadable = true
	}
	if written, err := s.store.ModTime(ctx, d); err == nil {
		meta.Written = written
	} else {
		meta.Unreadable = true
	}
	if meta.Unreadable {
		return meta
	}
	s.meta.store(key, meta)
	return meta
}
