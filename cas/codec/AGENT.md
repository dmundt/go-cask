# Agent instructions — `cas/codec`

This package-local guide applies to the codec layer under `cas/codec`.

## Purpose

The codec layer is the serialization boundary between the generic CAS core and the application payload format. Keep the core format-agnostic, keep codecs explicit, and keep every wrapper composable.

## Core rules

- Keep the core store format-agnostic: storage and identity are unaffected by codec choice.
- Prefer explicit encode/decode functions over runtime reflection. The package's exported API should remain typed and predictable.
- Every codec implements the same public contract: `Encode(T) ([]byte, error)` and `Decode([]byte) (T, error)`.
- Every codec that wraps another codec must support chaining through a `next` argument in its constructor.
- The wrapper order is: inner codec first, outer transform second. Example: `flate.New(gzip.New(json.New[T]()))`.
- Keep codec constructors consistent with the repo convention: `New(next, ...)` for wrapped codecs, and `NewRaw(...)`, `NewValue()`, or `NewMap()` for direct codecs when no inner codec is required.
- Use explicit constructor names that reflect the direct-vs-wrapper split. Direct codecs are not generic wrappers; they are a complete format implementation for T.
- Preserve deterministic output when a format is used for content-addressed data. Stable bytes matter more than convenience.
- Do not add hidden runtime type discovery or reflection to `cas` or `cas/*` packages. Keep conversion logic explicit and type-driven.
- Nil wrapped codecs must fail fast with a non-nil error; never silently fall through.
- For byte-transform wrappers, keep the transform separate from the actual value codec: the inner codec owns serialization semantics, the outer codec only changes representation.

## Documentation rules

- Keep README files short and package-focused.
- Explain the codec's role, packaging, and compatibility policy, not the full storage protocol.
- When a codec is compact or compatibility-only, say so explicitly.

## Scope

This file covers the codec family under `cas/codec` and the package-local rules for custom codec wrappers and chainable formats.
