---
type: Design Document
title: Object Descriptor + Sidecar Checksum — go-cask
description: Non-normative design note for storing a small object descriptor and optional sidecar checksum outside the object bytes, without changing the digest model or the Backend contract.
version: v4
---

# Object Descriptor + Sidecar Checksum — go-cask

Non-normative design sketch. Canonical rules remain in the spec set (`cas-core.md`, `operations.md`, `consistency.md`).

## 0. Status: implemented, with differences

The recorded-checksum path this note argued for is **shipped** as `cas/verify/sidecar`; the normative contract is `operations.md` §6, and this note is now its rationale. Where the sketch below differs from that contract, the contract wins. Differences, recorded rather than implied:

- **Placement:** the sketch puts the descriptor at the `Store[T]` layer (§3.2). The shipped layer is one rung lower — a `cas.Backend` decorator that composes with `cas.New`, `gitlike.NewRepository` and `internal/store.Open` unchanged, so it works for byte-layer consumers and needs no store change.
- **What is checksummed:** the sketch checksums the *logical payload* (`payload_checksum`); v1 checksums the **stored bytes** — exactly what `Backend.Get` returns — so it does not satisfy the "checksum independent of the raw object bytes" use case in §5. A logical-payload layer is a larger feature and stays deferred (operations §6.5).
- **Field names and encoding:** the shipped record uses `checksum_algo`/`checksum`/`size` with bare lowercase-hex digests (`encoding.TextMarshaler`); the sketch proposed `payload_checksum`/`payload_size` with an algorithm prefix.
- **`references`:** not in v1: only the typed layer knows `References()`, a byte-layer producer cannot derive it, and the object type stays the authoritative traversal source (the sketch says the same in §2.2).
- **Read path:** the sketch validates the descriptor inside `Store.Get`. The shipped read path is explicit and separate — `Rec.Verifier(...).Verify` / `VerifyAll` — so a read never pays for a check it did not ask for; `Get` delegates untouched.
- **Quarantine:** still not implemented (§3.3 says "quarantine + audit"): the shipped path reports a mismatch and never moves bytes (operations §6.3).

## 1. The constraint

The object digest is already the checksum: the key is the digest of the exact bytes written to the backend, and a digest is not part of the object payload. The core contract: `Backend` stores `Digest -> bytes`, while `Store[T]` owns the typed envelope, codec, and validation rules.

So a payload checksum must not be written into the object bytes themselves. Inject checksum metadata into the TLV payload and the payload changes, so the object's `Digest` changes too — a circular dependency, since the checksum is computed over bytes that include the checksum.

## 2. The design

Keep the object bytes canonical and store metadata in a separate sidecar descriptor.

### 2.1 Canonical object bytes

`Store[T]` still writes the canonical object bytes exactly as today:

- encode object to bytes via `Codec[T]`
- wrap the type/version envelope if the object uses the TLV framing
- compute `h := hasher.Digest(bytes)`
- write bytes to `Backend.Put(ctx, h, bytesReader)`

The digest is the only object identity key.

### 2.2 Sidecar descriptor

The descriptor is a small JSON/YAML/CBOR record stored next to the object in a metadata namespace. The store owns the descriptor; the backend does not.

Exact file layout:

```text
<base>/objects/<hex>          // current object bytes, one per digest
<base>/.meta/<hex>.json       // sidecar descriptor for that object
```

The descriptor is keyed by the same digest as the main object. Field names follow the sketch (the shipped record is `operations.md` §6.2; §0 lists the differences):

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
- `payload_checksum`: the checksum of the logical payload before the object is wrapped or otherwise normalized; for a whole-object store it may equal `digest` exactly.
- `references`: optional app-level reference list — metadata, not the authoritative reference graph; the type's `References()` method remains the authoritative traversal source.

The important rule: the descriptor is side data, never part of the object hash input.

## 3. Where it fits in the boundary

This sits at the `Store[T]` layer, not in the `Backend` interface. (The shipped layer sits one rung lower, as a `cas.Backend` decorator — §0.)

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

That is intentionally byte-only. It knows nothing about descriptors, envelopes, or object types.

### 3.2 Store boundary

`Store[T]` is the right insertion point because it already owns (the shipped decorator instead takes the caller's `Hasher`, because it sits below the store — §0):

- `Codec[T]`
- object validation
- `Type()` / `References()` semantics
- digest selection and verify-before-accept logic

The descriptor creation stays in a `Store` or `manifest`-like wrapper above the backend.

### 3.3 Read path

On read:

1. read object bytes from `Backend.Get(ctx, digest)`
2. if a descriptor exists, validate `digest` and `payload_checksum`
3. decode payload and validate the object (`Validate()` / `ErrCorrupt`)
4. return the typed object

On a mismatch:

- wrong `payload_checksum` → `ErrCorrupt`
- missing object bytes → `ErrNotFound`
- mismatch between descriptor and object bytes → quarantine + audit, never silently repair

## 4. Why this is the correct placement

This design preserves the repo's identity model:

- the digest remains the content key
- the object bytes remain canonical and hash-stable
- the descriptor is auxiliary metadata, not part of the content identity
- no backend write API changes
- the metadata can be optional, per store or per object family

It also keeps the boundary clean:

- backend: bytes and durability
- store: typed object semantics and validation
- descriptor: metadata and auditability
- sidecar checksum: optional verification of logical payloads; not a second identity key

## 5. When to use it

Use this pattern when you need one of the following:

- packaging metadata for a large or chunked object
- app-level provenance or audit info for a content-addressed blob
- a checksum independent of the raw object bytes for a logical payload layer — **not satisfied by v1**, which checksums the stored bytes (operations §6.2, and §0 above)
- a future object store that needs a manifest without changing the core backend contract

Do not use it when the object is a small whole-object blob whose digest already covers the exact stored bytes: there the descriptor is unnecessary overhead and the raw digest is sufficient.

## 6. Recommended rule for this repo

The repo should treat the descriptor as an optional metadata layer above `cas.Backend`, not as part of the core TLV object bytes: the object digest stays the source of truth, and the sidecar checksum is a convenience for integrity metadata and app-level verification, not a second canonical identity.
