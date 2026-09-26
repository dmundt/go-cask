---
type: Agent Instructions
title: Agent instructions — `cas/verify`
description: The package-local guide for cas/verify — keep integrity validation explicit and layered above the store, with caller-chosen hashers for checksum-addressed stores and the sidecar as the cheap second check over a strongly-addressed one.
version: v2
---

# Agent instructions — `cas/verify`

Package keeps integrity validation explicit + separate from storage model.

## Purpose

- Raw store remains `Digest -> bytes` backend.
- Caller chooses verification algorithm, passes it explicitly to `cas.Verify` or `cas.NewVerifier`.
- Maintenance helpers such as `cas/verify/crc32` are `cas.Hasher` implementations for a store deliberately addressed by that checksum — they address + validate same objects, cannot validate object addressed by another algorithm.
- `cas/verify/sidecar` is sibling maintenance layer for other direction: records a per-object checksum beside objects addressed by a strong hash, validates stored bytes against that record, so a checksum can act as cheap second check without changing object's address (operations §6).

## Rules

- Never change backend contract or object-address semantics.
- Keep verification layered above store, not inside it.
- Checksum hasher verifies only objects addressed with same checksum: `cas.Verify` compares recomputed digest to object's address, so never document or test one as cheap check over strongly-addressed store. Cheap check is `cas/verify/sidecar`'s job — compares against recorded checksum, never against address (`cas/verify/README.md`).
- Prefer caller-controlled `Hasher` implementations + explicit helper packages.
- Helper tagged maintenance check: say so clearly in docs + examples.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification + required checks pass.
