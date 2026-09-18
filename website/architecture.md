# Architecture

CASK is deliberately layered.

## Core layers

1. `cas` — the generic, app-agnostic storage core
2. storage backends — raw byte stores such as filesystem and memory backends
3. typed object layers — application-specific `Object[T]`, `Codec[T]`, and `Store[T]`
4. helper packages and examples — compression, pack helpers, and example apps

## Design principle

The content digest defines identity. Data integrity is verified with a caller-supplied hash strategy, while the backend remains a raw `Digest -> bytes` storage layer.

This split keeps the core simple:

- hashing belongs to the client
- serialization belongs to the codec
- storage belongs to the backend
- validation is explicit and opt-in

## Typical data flow

```mermaid
flowchart LR
    A[Application object] --> B[Codec[T]]
    B --> C[Bytes]
    C --> D[Backend]
    A --> E[Hasher / verifier]
    E --> C
```

This makes the flow explicit: the client chooses the hash strategy, the codec determines serialization, and the backend stores the bytes.

## Reference model

The `gitlike` package provides a usage pattern for building typed graphs like blobs, trees, commits, and tags without contaminating the generic `cas` core.

This is the project’s default way to teach and reuse object-model patterns without forcing them into the core library itself.
