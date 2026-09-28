# bloom — advisory Bloom guard for hot-path existence checks

**What it demonstrates.** An optional Bloom filter in front of a backend, speeding negative
existence checks without becoming the authority on object truth: a negative result may
short-circuit a lookup, a positive one is a hint that still needs backend verification.

## `cas` core parts used

| Component | Where |
|---|---|
| `cas.Digest` / `cas.NewDigest` | the object identities used in the demo |
| `cas.Backend` | the in-memory backend beneath the guard |
| `bloom.NewGuard` | wraps the backend with Bloom-assisted `Exists` checks |
| `bloom/standard` filter | in-memory probabilistic pre-check |
| custom `IndexHash` | deterministic Bloom indexing for a different bit pattern |

## What it extends

- **Optional acceleration** — outside the CAS core, advisory: faster hot-path negative
  lookups, no change to identity, algorithm, or correctness.
- **Custom indexing** — `customIndexHash` shows the Bloom index is a local detail, not part of
  the digest contract.
- **Guard semantics** — `Put` adds to the filter; `Exists` short-circuits a negative and
  confirms a positive with the backend.

## Code walkthrough

- `main.go` — in-memory backend, standard Bloom filter with a custom index function wrapped in
  `bloom.NewGuard`, stores a digest, demonstrates both lookup paths.

## How to run

```text
go run ./examples/bloom
go test ./examples/bloom/...
```

Prints whether the stored digest is likely present, whether the backend confirms it, and
whether an absent digest is rejected before the backend is consulted.
