# mem — in-memory backend

Package `mem` — in-memory backend for the generic `cas` core: tests, benchmarks, examples, and
short-lived workloads. Fast and deterministic, but not persistent, and no replacement for a
durable backend.

Its clause is `memory`, which `cas/cache/mem` also declares, so this repository imports it as
`backmem` and the cache as `cachemem` (`cas/AGENT.md`); no file imports either one unaliased.

```go
backend := backmem.New()
```

## Snapshots

`Snapshot` and `Restore` capture and replay raw backend state:

```go
var snapshot bytes.Buffer
if err := backend.Snapshot(ctx, &snapshot); err != nil {
    // handle error
}
if err := backend.Restore(ctx, &snapshot); err != nil {
    // handle error
}
```

The snapshot format is deterministic, versioned, binary, and specific to this backend. Records
contain raw digests and payloads; typed codecs and hashers are not involved. `Restore` validates
the complete input before replacing state, and a configured `WithMaxSize` limit applies. Treat
snapshots as test, replay, and diagnostic artifacts, not as a cross-version backup format.

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

Portable import writes objects through the destination backend and therefore does not provide
atomic replacement if a later record fails.
