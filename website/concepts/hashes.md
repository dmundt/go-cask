# Hashes

A hash turns bytes into a stable object identifier. go-cask's core names no
algorithm — a `Digest` is just bytes — so the algorithm is a property of the
client, injected as a `Hasher`.

## Shipped hashers

| Package | Algorithm | Notes |
|---|---|---|
| `cas/hash/sha256` | SHA-256 | the project's recommended default for new data |
| `cas/hash/sha512` | SHA-512 | full-width alternative |
| `cas/hash/sha512_256` | SHA-512/256 | fast, secure alternative to SHA-256 |

MD5 and SHA-1 are not shipped; they are legacy/compatibility-only choices and
are not recommended for new content-addressed data.

## Custom algorithms

`Hasher` is a two-method interface:

```go
package hashers

import (
    "io"

    "github.com/dmundt/go-cask/cas"
)

// Hasher turns a stream of bytes into the Digest that addresses it, and
// validates a Digest a caller passed in.
type Hasher interface {
    Digest(r io.Reader) (cas.Digest, error)
    Validate(d cas.Digest) error
}

// The contract above and the shipped cas.Hasher accept exactly each other's
// implementations, so this listing cannot drift from the real interface.
var (
    _ cas.Hasher = Hasher(nil)
    _ Hasher     = cas.Hasher(nil)
)
```

Any algorithm that fits this shape works — BLAKE3, a truncated digest, or a
non-cryptographic key for tests — without changing the core or any backend.

## Why the core stays algorithm-agnostic

The storage layer stores bytes under a digest and does not care which
algorithm produced it. That keeps `cas` reusable across trust models instead
of baking one hashing policy into the storage contract.

## Identity and verification are different concerns

A digest answers "what is this object?" Verification answers "does the
stored content still match?" `cas.Verify` / `cas.NewVerifier` re-read an
object and recompute its digest with the caller's `Hasher`; nothing verifies
automatically on every `Get`.
