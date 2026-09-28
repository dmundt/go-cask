# Examples — go-cask

Runnable reference programs under `examples/` on the generic `cas` core: application-layer
teaching code and app wiring, never the library core.

## Included examples

| Example | Pattern |
|---|---|
| [api](./api/README.md) | HTTP exposure for a CAS-backed service |
| [artifacts](./artifacts/README.md) | typed artifacts with codecs and caching |
| [bloom](./bloom/README.md) | optional Bloom guard for negative existence checks |
| [files](./files/README.md) | file-oriented object store on the byte backend |
| [notes](./notes/README.md) | note/object graph on the Git-like reference model |
| [pack](./pack/README.md) | fixed-size chunking and manifest metadata helper |

## Design

They pick a durable or in-memory backend, choose a hash via `cas.Hasher` and a codec via
`Codec[T]`, build typed objects and references, and add caching or prefetch layers when
read-heavy workloads justify it.

## Policy

- `cas` stays algorithm- and format-agnostic; the examples are teaching patterns, not the core.
- Secure defaults for new work: `SHA-256` identity, JSON formats, `fs` persistence.

Run all from the repo root: `go run ./cmd/buildtool run-examples`; one at a time:
`go run ./examples/...`, or the per-example `go run ./examples/<name>` path in its README,
which also carries a "What it demonstrates" section.
