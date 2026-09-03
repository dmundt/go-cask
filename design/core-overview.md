---
title: Core Overview — go-cask
description: One minimal diagram of the cas core interfaces and their dependencies/relationships — a quick orientation aid; the normative architecture is cas-core.instructions.md §3.
version: v1
---

# Core Overview — go-cask

> A single-picture orientation to the `cas` core: the interfaces and how
> they depend on each other. **Not normative** — the authoritative
> architecture, contracts, and aspect diagrams live in
> `.github/instructions/cas-core.instructions.md` §3 (this diagram is the
> same as the §3.3 overview).

```mermaid
classDiagram
    direction LR

    class Hash {
        <<interface>>
        +Algorithm() string
        +String() string
        +Equal(other Hash) bool
    }
    class RawStore {
        <<interface>>
        +Put(ctx, h, r) error
        +Get(ctx, h) io.ReadCloser
        +Exists(ctx, h) (bool, error)
        +Delete(ctx, h) error
        +List(ctx, algo) []Hash
    }
    class FSRawStore {
        <<backend>>
    }
    class MemoryRawStore {
        <<backend>>
    }
    RawStore <|.. FSRawStore : implements
    RawStore <|.. MemoryRawStore : implements

    class Object~T~ {
        <<interface>>
        +Type() string
        +References() []Hash
    }
    class Codec~T~ {
        <<interface>>
        +Encode(v T) ([]byte, error)
        +Decode(data []byte) (T, error)
    }
    class Store~T~ {
        +Put(ctx, obj T) (Hash, error)
        +Get(ctx, h) (T, error)
        +Delete(ctx, h) error
    }
    class Walker~T~ {
        +Walk(ctx, h) error
    }
    Store~T~ o-- RawStore : raw
    Store~T~ o-- Codec~T~ : codec
    Store~T~ ..> Object~T~ : stores
    Walker~T~ ..> Store~T~ : reads via Get

    class CachedStore~T~
    class LRUCache~T~
    CachedStore~T~ o-- Store~T~ : wraps
    LRUCache~T~ --|> CachedStore~T~ : extends
```

Reading the diagram:

- **Byte layer (non-generic).** `RawStore` is the storage contract; the
  backends implement it (`FSRawStore` reference, `MemoryRawStore` tests).
  `Hash` addresses everything.
- **Typed layer (generic, constrained).** `Store[T]` is the hub: it composes
  a `RawStore` (where bytes live) and a `Codec[T]` (how values serialize),
  and by constraint (`Store[T Object[T]]`) every value it handles is an
  `Object[T]`. `Walker[T]` traverses object graphs through a `Store[T]` via
  `Get` — no domain knowledge in the core.
- **Caching.** `CachedStore[T]` wraps `Store[T]` (lazy `CachedObject[T]`
  proxies); `LRUCache[T]` extends `CachedStore[T]` with a size bound.

Apps never touch the byte layer directly; they define `Object[T]` types and
use per-type `Store[T]` instances (the `examples/gitlike` model is the
reference).
