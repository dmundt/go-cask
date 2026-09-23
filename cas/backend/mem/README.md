# mem — in-memory backend

Package `mem` provides the in-memory backend for the generic `cas` core.

It is intended for tests, benchmarks, examples, and short-lived workloads. It is fast and deterministic, but it is not persistent and does not replace a durable backend.

## Policy

- The backend stores raw bytes by digest; it does not know the object type or hash algorithm.
- The caller injects the hash algorithm through `cas.Hasher`.
- The caller injects the object codec through `Codec[T]`.

## Typical use

```go
backend := mem.New()
```

This backend is excellent for local experiments, benchmark baselines, and unit tests that need a clean store without disk I/O.

## Snapshots

Use `Snapshot` and `Restore` to capture and replay raw backend state:

```go
var snapshot bytes.Buffer
if err := backend.Snapshot(ctx, &snapshot); err != nil {
    // handle error
}
if err := backend.Restore(ctx, &snapshot); err != nil {
    // handle error
}
```

The snapshot format is deterministic, versioned, binary, and specific to this
backend. Records contain raw digests and payloads; typed codecs and hashers are
not involved. `Restore` validates the complete input before replacing state,
and a configured `WithMaxSize` limit applies. Treat snapshots as test,
replay, and diagnostic artifacts, not as a cross-version backup format.

For transfer between memory, filesystem, and other backends, use the portable
`cas/backend/snapshot` package:

```go
if err := snapshot.Export(ctx, raw, writer); err != nil {
    // handle error
}
if err := snapshot.Import(ctx, destination, reader); err != nil {
    // handle error
}
```

Portable import writes objects through the destination backend and therefore
does not provide atomic replacement if a later record fails.
