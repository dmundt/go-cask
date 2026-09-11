# cache

The cache layer wraps the typed `Store[T]` surface with lazy loading and retention policies. It sits above the byte backend and typed store layer while staying generic over the stored object type.

## Included implementations

- [mem](./mem/README.md) — in-memory cached store/object wrapper
- [lru](./lru/README.md) — bounded LRU cache for typed values
- [prefetch](./prefetch/README.md) — prefetch-oriented wrapper for read-heavy graphs

## Policy

- Caching is an optimization layer, not part of the store's identity model.
- Hash choice stays with the caller via `cas.Hasher`.
- Codec choice stays with the caller via `Codec[T]`.
- A cache may change latency and memory use without changing the underlying content-addressed semantics.

## Typical use

- Use `lru` for bounded retention of hot objects.
- Use `mem` for a simple in-memory cache.
- Use `prefetch` when object-walk workloads benefit from read-ahead behavior.

## Notes

A cache may evict entries without changing the underlying store. The value remains addressable by digest in the base store.
