# Agent instructions — `cas/verify`

This package keeps integrity validation explicit and separate from the storage model.

## Purpose

- The raw store remains a `Digest -> bytes` backend.
- The caller chooses the verification algorithm and passes it explicitly to `cas.Verify` or `cas.NewVerifier`.
- Maintenance helpers such as `cas/verify/crc32` provide cheap, optional consistency checks without redefining object identity.

## Rules

- Do not change the backend contract or object-address semantics.
- Keep verification layered above the store, not inside it.
- Prefer caller-controlled `Hasher` implementations and explicit helper packages.
- If a helper is tagged as a maintenance check, say so clearly in docs and examples.
