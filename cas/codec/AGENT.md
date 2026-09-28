---
type: Agent Instructions
title: Agent instructions — `cas/codec`
description: The package-local guide for the codec layer under cas/codec — the Encode/Decode contract, cascadeable wrappers with the inner codec first, constructor naming, deterministic bytes for content-addressed data, and no runtime type discovery.
version: v3
---

# Agent instructions — `cas/codec`

## Purpose

Codec layer = serialization boundary between generic CAS core and application payload format.
Keep core format-agnostic, codecs explicit, every wrapper composable.

## Core rules

- Keep core store format-agnostic: storage and identity unaffected by codec choice.
- Every codec implements same public contract: `Encode(T) ([]byte, error)` and `Decode([]byte) (T, error)`.
- Prefer explicit encode/decode functions over runtime reflection. Exported API stays typed and predictable.
- Every codec that wraps another codec supports chaining through the `next` argument in its constructor.
- Wrapper order: inner codec first, outer transform second — `flate.New(gzip.New(json.New[T]()))`.
- Constructor convention: `New(next, ...)` for wrapped codecs, `NewRaw(...)`, `NewValue()`, `NewMap()` for direct codecs that need no inner codec. Names reflect the direct-vs-wrapper split: a direct codec is a complete format implementation for `T`, not a generic wrapper.
- Byte-transform wrappers keep the transform separate from the value codec: inner codec owns serialization semantics, outer codec only changes representation.
- Nil wrapped codec fails fast with a non-nil error; never silently fall through.
- Preserve deterministic output when the format is used for content-addressed data — stable bytes matter more than convenience.
- Never add hidden runtime type discovery or reflection to `cas` or `cas/*` packages. Conversion logic stays explicit and type-driven.

## Documentation rules

- READMEs short and package-focused: codec's role, packaging, compatibility policy — never the full storage protocol.
- Codec compact or compatibility-only: say so explicitly.

## Scope

File covers codec family under `cas/codec` and package-local rules for custom codec wrappers and
chainable formats.

## Signed pull-request workflow

Signed commits required: rebuild PR branches locally from current `main`; never GitHub's
server-side rebase or update-branch. Apply changes with `git cherry-pick -S`, verify every head
commit with `git verify-commit`, push with `git push --force-with-lease`. Enable auto-merge only
after signature verification and required checks pass.
