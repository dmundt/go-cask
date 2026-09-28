---
type: Design Document
title: Object Descriptor + Sidecar Checksum — go-cask
description: Non-normative design note for storing a small object descriptor and optional sidecar checksum outside the object bytes, without changing the digest model or the Backend contract.
version: v5
---

# Object Descriptor + Sidecar Checksum — go-cask

Non-normative sketch and rationale for `cas/verify/sidecar`. Canonical rules:
`cas-core.md`, `operations.md`, `consistency.md`.

## 0. Status: implemented, with differences

- Recorded-checksum path **shipped** as `cas/verify/sidecar`.
- Normative contract `operations.md` §6; this note is its rationale; the contract
  wins on any difference.
- **Placement:** sketch → `Store[T]` layer (§3.2); shipped → one rung lower, a
  `cas.Backend` decorator; `cas.New`, `gitlike.NewRepository`,
  `internal/store.Open` unchanged; serves byte-layer consumers, no store change.
- **Checksummed:** sketch → *logical payload* (`payload_checksum`); v1 → **stored
  bytes**, exactly what `Backend.Get` returns, so the §5 "independent of the raw
  object bytes" case is unmet; logical-payload layer stays deferred
  (`operations.md` §6.5).
- **Fields:** shipped `checksum_algo`/`checksum`/`size` with bare lowercase-hex
  digests (`encoding.TextMarshaler`); sketch `payload_checksum`/`payload_size`
  with an algorithm prefix.
- **`references`:** not in v1 — only the typed layer knows `References()`; the
  object type stays the authoritative traversal source (sketch §2.2 agrees).
- **Read path:** sketch → validate inside `Store.Get`; shipped → explicit,
  separate `Rec.Verifier(...).Verify` / `VerifyAll`; a read never pays for an
  unasked check; `Get` delegates untouched.
- **Quarantine:** not implemented (§3.3 says "quarantine + audit"); the shipped
  path reports a mismatch and never moves bytes (`operations.md` §6.3).

## 1. The constraint

- `Backend` stores `Digest -> bytes`; `Store[T]` owns typed envelope, codec,
  validation.
- Digest is already the checksum: key = digest of the exact bytes written; a
  digest is not part of the object payload.
- Checksum metadata in the TLV payload changes the payload, hence the `Digest` —
  circular (the checksum covers bytes including the checksum).

## 2. The design

- Object bytes stay canonical; metadata lives in a separate sidecar descriptor.

### 2.1 Canonical object bytes

`Store[T]` still writes the canonical object bytes exactly as today:

- encode object to bytes via `Codec[T]`
- wrap the type/version envelope if the object uses the TLV framing
- compute `h := hasher.Digest(bytes)`
- write bytes to `Backend.Put(ctx, h, bytesReader)`

The digest is the only object identity key.

### 2.2 Sidecar descriptor

- Small JSON/YAML/CBOR record beside the object, in a metadata namespace.
- Store owns it; backend does not; keyed by the same digest as the object.
- Field names follow the sketch; shipped record `operations.md` §6.2 (§0).

Exact file layout:

```text
<base>/objects/<hex>          // current object bytes, one per digest
<base>/.meta/<hex>.json       // sidecar descriptor for that object
```

```json
{
  "version": 1,
  "digest": "sha256:abcd...",
  "type": "blob@1",
  "codec": "json",
  "payload_checksum": "sha256:abcd...",
  "payload_size": 4096,
  "created_at": "2026-09-16T22:45:00Z",
  "references": ["sha256:dead...", "sha256:beef..."]
}
```

Rules:

- `digest`: the canonical object key, the address of the backend blob.
- `type`: the logical object type (`blob@1`, `tree@1`, `commit@1`, etc.).
- `codec`: the serialization scheme used for the logical payload.
- `payload_checksum`: logical-payload checksum before wrapping or other
  normalization; may equal `digest` for a whole-object store.
- `references`: optional app-level list — metadata, not the authoritative
  reference graph; the type's `References()` method stays authoritative.
- The descriptor is side data, never part of the object hash input.

## 3. Where it fits in the boundary

- Sketch layer: `Store[T]`, not the `Backend` interface; shipped one rung lower,
  a `cas.Backend` decorator (§0).

### 3.1 Backend boundary

`Backend` remains the raw contract:

```go
type Backend interface {
    Put(ctx context.Context, d Digest, r io.Reader) error
    Get(ctx context.Context, d Digest) (io.ReadCloser, error)
    Exists(ctx context.Context, d Digest) (bool, error)
    Delete(ctx context.Context, d Digest) error
    List(ctx context.Context) ([]Digest, error)
    Stats(ctx context.Context) (*Stats, error)
}
```

Byte-only: no descriptors, envelopes, or object types.

### 3.2 Store boundary

- `Store[T]` already owns `Codec[T]`, validation, `Type()`/`References()`, digest
  selection, verify-before-accept.
- Shipped decorator takes the caller's `Hasher`, sitting below the store (§0).
- Descriptor creation lives in a `Store` or `manifest`-like wrapper above the
  backend.

### 3.3 Read path

On read:

1. read object bytes from `Backend.Get(ctx, digest)`
2. if a descriptor exists, validate `digest` and `payload_checksum`
3. decode payload and validate the object (`Validate()` / `ErrCorrupt`)
4. return the typed object

On a mismatch:

- wrong `payload_checksum` → `ErrCorrupt`
- missing object bytes → `ErrNotFound`
- descriptor/object mismatch → quarantine + audit, never silently repair

## 4. Why this is the correct placement

- Digest stays the content key; object bytes stay canonical and hash-stable.
- Descriptor is auxiliary metadata, not content identity.
- No backend write API changes; metadata optional per store or per object family.
- Boundaries: backend = bytes/durability; store = typed semantics/validation;
  descriptor = metadata/auditability; sidecar checksum = optional logical-payload
  verification, not a second identity key.

## 5. When to use it

Use for one of:

- packaging metadata for a large or chunked object
- app-level provenance or audit info for a content-addressed blob
- a checksum independent of the raw object bytes for a logical payload layer —
  **not satisfied by v1**, which checksums the stored bytes (`operations.md` §6.2,
  §0)
- a future object store needing a manifest without changing the core backend
  contract

Not for a small whole-object blob whose digest already covers the exact stored
bytes: descriptor overhead, raw digest sufficient.

## 6. Recommended rule for this repo

- Descriptor = optional metadata layer above `cas.Backend`, not part of the core
  TLV object bytes.
- Object digest stays the source of truth.
- Sidecar checksum = convenience for integrity metadata and app-level
  verification, not a second canonical identity.
