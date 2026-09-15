# bloom — advisory Bloom guard for hot-path existence checks

**What it demonstrates.** An optional Bloom filter that sits in front of a backend and speeds up negative existence checks without becoming the authority on object truth. The backend remains the source of truth: a negative Bloom result can short-circuit a lookup, but a positive Bloom result is only a hint that still requires backend verification.

## `cas` core parts used

| Component | Where |
|---|---|
| `cas.Digest` / `cas.NewDigest` | the object identities used in the demo |
| `cas.Backend` | the in-memory backend beneath the guard |
| `bloom.NewGuard` | wraps the backend with Bloom-assisted `Exists` checks |
| `bloom/standard` filter | in-memory probabilistic pre-check |
| custom `IndexHash` | deterministic Bloom indexing for a different bit pattern |

## What it extends

- **Optional acceleration layer** — Bloom lives outside the CAS core and is advisory by design. It improves hot-path negative lookup performance but does not change identities, algorithms, or correctness guarantees.
- **Custom indexing** — the example defines a `customIndexHash` to show that a Bloom index is a local implementation detail, not part of the CAS digest contract.
- **Guard semantics** — `Put` adds to the filter, `Exists` short-circuits on a negative result, and positive results still call the backend for confirmation.

## Code walkthrough

- `main.go` — builds an in-memory backend, creates a standard Bloom filter with a custom index function, wraps it in `bloom.NewGuard`, stores a digest, and demonstrates both the positive and negative lookup paths.

## How to run

```text
go run ./examples/bloom
go test ./examples/bloom/...
```

The demo prints whether the stored digest is likely present, whether the backend confirms the object exists, and whether an absent digest is rejected before the backend is consulted.
