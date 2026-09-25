---
type: Agent Instructions
title: Agent instructions — `cas/verify`
description: The package-local guide for cas/verify — keep integrity validation explicit and layered above the store, with caller-chosen hashers for checksum-addressed stores and the sidecar as the cheap second check over a strongly-addressed one.
version: v1
---

# Agent instructions — `cas/verify`

This package keeps integrity validation explicit and separate from the storage model.

## Purpose

- The raw store remains a `Digest -> bytes` backend.
- The caller chooses the verification algorithm and passes it explicitly to `cas.Verify` or `cas.NewVerifier`.
- Maintenance helpers such as `cas/verify/crc32` are `cas.Hasher` implementations for a store deliberately addressed by that checksum — they address and validate the same objects, and cannot validate an object addressed by another algorithm.
- `cas/verify/sidecar` is the sibling maintenance layer for the other direction: it records a per-object checksum beside objects addressed by a strong hash and validates the stored bytes against that record, so a checksum can act as a cheap second check without changing the object's address (operations §6).

## Rules

- Do not change the backend contract or object-address semantics.
- Keep verification layered above the store, not inside it.
- A checksum hasher verifies only objects addressed with that same checksum: `cas.Verify` compares the recomputed digest to the object's address, so never document or test one as a cheap check over a strongly-addressed store. The cheap check is `cas/verify/sidecar`'s job — it compares against the recorded checksum, never against the address (`cas/verify/README.md`).
- Prefer caller-controlled `Hasher` implementations and explicit helper packages.
- If a helper is tagged as a maintenance check, say so clearly in docs and examples.

## Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
