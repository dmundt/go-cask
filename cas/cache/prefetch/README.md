# prefetch — read-ahead cache wrapper

Package `prefetch` provides a prefetch-on-access cache for the generic `cas` core: wraps a cached
store and warms nearby references ahead of later reads. For object graphs or tree-like structures
where traversal is likely to continue through known references. Optional and best-effort.

```go
cache := prefetch.NewSmartCache(store, 2)
obj, err := cache.GetWithPrefetch(ctx, digest)
```
