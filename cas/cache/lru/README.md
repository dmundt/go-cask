# lru — bounded LRU cache

Package `lru` provides a size-bounded LRU cache for the generic `cas` core.

It wraps a typed `Store[T]` and keeps the most recently used entries hot, evicting the least-recently-used object when the cap is reached. It is an optimization layer, not part of the content-addressing model itself.

## Policy

- Caches are optional and must not change object identity or storage semantics.
- The caller chooses the hash algorithm and object codec.
- LRU retention is a performance policy, not a semantic requirement.

## Typical use

```go
cache, err := lru.New(store, 1024)
```

Use it when read-heavy workloads benefit from bounded retention of recent objects.
