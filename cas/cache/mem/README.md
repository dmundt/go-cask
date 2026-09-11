# mem — cached store wrapper

Package `mem` provides the in-memory cached-store implementation used by the generic `cas` core.

It adds lazy loading and caching semantics on top of a typed store so repeated reads can reuse already-loaded values without re-decoding the object. This is a read-performance optimization, not a change to object identity.

## Policy

- Caches may evict or reload values without affecting the underlying store.
- The core keeps the same content-addressed semantics regardless of cache policy.
- The caller chooses the hash algorithm and codec; the cache does not define either.

## Typical use

```go
cached := mem.New(store)
```

This is the simplest cache wrapper and a good base for other cache policies or prefetch strategies.
