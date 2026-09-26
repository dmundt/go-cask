---
type: Agent Instructions
title: Agent instructions — `cas/codec`
description: The package-local guide for the codec layer under cas/codec — the Encode/Decode contract, cascadeable wrappers with the inner codec first, constructor naming, deterministic bytes for content-addressed data, and no runtime type discovery.
version: v2
---

# Agent instructions — `cas/codec`

Package-local guide applies to codec layer under `cas/codec`.

## Purpose

Codec layer = serialization boundary between generic CAS core and application payload format. Keep core format-agnostic, codecs explicit, every wrapper composable.

## Core rules

- Keep core store format-agnostic: storage and identity unaffected by codec choice.
- Prefer explicit encode/decode functions over runtime reflection. Package's exported API must remain typed and predictable.
- Every codec implements same public contract: `Encode(T) ([]byte, error)` and `Decode([]byte) (T, error)`.
- Every codec that wraps another codec must support chaining through `next` argument in its constructor.
- Wrapper order: inner codec first, outer transform second. Example: `flate.New(gzip.New(json.New[T]()))`.
- Keep codec constructors consistent with repo convention: `New(next, ...)` for wrapped codecs, `NewRaw(...)`, `NewValue()`, `NewMap()` for direct codecs when no inner codec required.
- Use explicit constructor names that reflect direct-vs-wrapper split. Direct codecs not generic wrappers; complete format implementation for T.
- Preserve deterministic output when format used for content-addressed data. Stable bytes matter more than convenience.
- Never add hidden runtime type discovery or reflection to `cas` or `cas/*` packages. Keep conversion logic explicit and type-driven.
- Nil wrapped codecs must fail fast with non-nil error; never silently fall through.
- Byte-transform wrappers: keep transform separate from actual value codec: inner codec owns serialization semantics, outer codec only changes representation.

## Documentation rules

- Keep README files short and package-focused.
- Explain codec's role, packaging, compatibility policy — not full storage protocol.
- Codec compact or compatibility-only: say so explicitly.

## Scope

File covers codec family under `cas/codec` and package-local rules for custom codec wrappers and chainable formats.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
