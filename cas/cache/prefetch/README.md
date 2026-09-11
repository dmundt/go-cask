# prefetch — read-ahead cache wrapper

Package `prefetch` provides a prefetch-on-access cache for the generic `cas` core.

It wraps a cached store and warms nearby references ahead of later reads. This is useful for object graphs or tree-like structures where traversal is likely to continue through known references.

## Policy

- Prefetch is optional and best-effort.
- It must never change object identity or create storage semantics outside the underlying store.
- The caller still owns both the hash algorithm and codec choice.

## Typical use

```go
cache := prefetch.NewSmartCache(store, 2)
obj, err := cache.GetWithPrefetch(ctx, digest)
```

Use it when read-heavy workloads repeatedly traverse object graphs and benefit from warm caches.
