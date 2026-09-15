# chunk — large-payload splitting utilities

Package `chunk` provides simple helpers for splitting and reassembling byte payloads into fixed-size chunks. It is a workload-oriented utility for large-object workflows and does not change the content-addressed identity model in the `cas` core.

## Policy

- The core keeps object identity and digest semantics unchanged.
- `chunk` is a helper package for large payload work, not a new canonical object model.
- It is useful for app-level chunking strategies when a store wants to process large blobs incrementally.

## Typical use

```go
parts := chunk.Split(data, 64*1024)
rebuilt := chunk.Join(parts)
```

Use it when large payloads need to be segmented for staging, streaming, or operational workflows without changing the underlying CAS semantics.
