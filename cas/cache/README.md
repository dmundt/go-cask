# cache

Wraps the typed `Store[T]` surface with lazy loading and retention policies. Sits above the byte
backend and typed store layer, generic over the stored object type.

## Included implementations

- [mem](./mem/README.md) — in-memory cached store/object wrapper
- [lru](./lru/README.md) — bounded LRU cache for typed values
- [prefetch](./prefetch/README.md) — prefetch-oriented wrapper for read-heavy graphs

## Policy

- A cache MUST NOT change object identity or create storage semantics outside the underlying store:
  it is an optimization layer, changing latency and memory use, never content-addressed semantics.
  It may evict or reload entries; the value stays addressable by digest in the base store.
- Hash choice stays with the caller via `cas.Hasher`; codec choice via `Codec[T]`.
- Selection: `lru` for bounded retention of hot objects, `mem` for a simple in-memory cache,
  `prefetch` when object-walk workloads benefit from read-ahead behavior.
