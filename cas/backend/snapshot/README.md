# snapshot — portable backend archives

Package `snapshot` transfers raw content-addressed objects between any
backends implementing the `cas.Backend` interface.

## Use

Export an archive from one backend and import it into another:

```go
var archive bytes.Buffer
if err := snapshot.Export(ctx, source, &archive); err != nil {
    // handle error
}
if err := snapshot.Import(ctx, destination, &archive); err != nil {
    // handle error
}
```

The package operates below typed stores. It does not invoke a `cas.Codec[T]`,
compute digests, or select a hash algorithm. The archive preserves each raw
digest and its payload.

## Format and behavior

- Versioned binary format.
- Deterministic record order by digest.
- Streaming archive output and context-aware I/O.
- Invalid, duplicate, truncated, and trailing records are rejected.
- `Import` writes through `Backend.Put`.
- Generic imports are not atomic; an error after earlier records are written
  can leave partial destination state.

For atomic memory-backend replacement, use
`mem.Backend.Snapshot` and `mem.Backend.Restore` instead. Those methods use a
backend-specific format and validate the complete input before replacing the
in-memory map.

Snapshots are intended for tests, replay, diagnostics, and backend migration.
They are not a durable backup contract across format versions.
