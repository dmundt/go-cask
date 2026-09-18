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

## Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
