# Examples — go-cask

The `examples/` directory contains runnable reference programs that show how to build real applications on top of the generic `cas` core.

## Included examples

- [api](./api/README.md) — small HTTP-exposure pattern for a CAS-backed service
- [artifacts](./artifacts/README.md) — example of storing typed artifact objects with codecs and caching
- [bloom](./bloom/README.md) — optional Bloom-guard example for fast negative existence checks
- [files](./files/README.md) — example of a file-oriented object store on top of the byte backend
- [notes](./notes/README.md) — minimal note/object graph example using the Git-like reference model
- [pack](./pack/README.md) — fixed-size chunking and manifest metadata helper example

## Design

These examples are intentionally application-layer code. They show how to:

- pick a durable or in-memory backend
- choose a hash algorithm via `cas.Hasher`
- choose a codec via `Codec[T]`
- build typed objects and references
- add caching or prefetch layers when read-heavy workloads justify it

## Policy

- `cas` remains algorithm- and format-agnostic.
- Examples use secure defaults for new work: `SHA-256` for identity, JSON for readable formats, and `fs` for persistent storage.
- The examples are not the library core; they are teaching patterns and application wiring.

Run them from the repo root with `./scripts/run-examples.sh` or `go run ./examples/...`; each example also has a one-command `go run ./examples/<name>` path in its README and a short "What it demonstrates" section so the proof point is visible before you run it.
