# Cache layer — go-cask

See also: [cas/README.md](../README.md) for the wider package architecture and supported policy.

The cache layer wraps the typed `Store[T]` surface with lazy loading and in-memory retention policies. It sits on top of the byte backend and typed store layer, and remains generic over the stored object type.

## What lives here

- [mem/](./mem) — in-memory cached store/object wrapper
- [lru/](./lru) — bounded LRU cache for typed object values
- [prefetch/](./prefetch) — prefetch-friendly cache wrapper for read-heavy workloads

## Policy

- Caches are an optimization layer, not part of the core identity model.
- The hash algorithm remains chosen by the client through `cas.Hasher`.
- The object codec remains chosen by the client through `Codec[T]`.
- Caching may change read latency and memory footprint, but never the underlying content-addressed semantics.

## Recommended use

- Use `lru` when you want bounded retention of hot typed objects.
- Use `mem` when a simple object cache is enough.
- Use `prefetch` when read-heavy workflows benefit from prefetch-on-access behavior.
- Keep cache sizes explicit and bounded; the cache must not make storage semantics or object identity ambiguous.

## Notes

- A cache may evict objects without changing the underlying store.
- The underlying object value remains addressable by digest in the base store.
- Cache wrappers are optional; they improve performance without changing the core data model.
