# mem — in-memory backend

Package `mem` provides the in-memory backend for the generic `cas` core.

It is intended for tests, benchmarks, examples, and short-lived workloads. It is fast and deterministic, but it is not persistent and does not replace a durable backend.

## Policy

- The backend stores raw bytes by digest; it does not know the object type or hash algorithm.
- The caller injects the hash algorithm through `cas.Hasher`.
- The caller injects the object codec through `Codec[T]`.

## Typical use

```go
raw := mem.New()
```

This backend is excellent for local experiments, benchmark baselines, and unit tests that need a clean store without disk I/O.
