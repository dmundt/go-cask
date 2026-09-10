---
type: Specification
title: Object Versioning — go-cask
description: Semantic versioning for object models — versioned type names, registry and resolution of multiple model versions, compatibility rules, and migration; the 4th, independent version space of go-cask.
version: v5
---

# Object Versioning — go-cask

Object models evolve; this is their **semantic versioning** contract: a versioned type name, coexistence of model versions in one store, compatibility rules — so a model change never makes old data unreadable. Related: `cas-core.md` §4.7 (`Object[T]`), §4.12 (gitlike); `library-design.md` §2 (errors); `versioning.md` §6 (other version spaces); `consistency.md` (migration safety).

## 1. The version space

Object-model versions are a **fourth, independent version space** — separate from library Git tags, HTTP API majors, and doc revisions (versioning §6): scheme `type@major` in `Type()`, independent of library semver/HTTP API/docs. An app MAY bump its object model without a library release; a library release MAY add object types without an object-model bump.

## 2. Versioned type names

`Object[T].Type()` returns `<type>@<major>` (gitlike: `blob@1`, `tree@1`, `commit@1`, `tag@1`).

- The major is part of the type identity: the versioned name is written into the TLV envelope's Type field (`cas/envelope.go`; cas-core §8 decision 1), required regardless of codec.
- The address is unaffected — it is the raw digest bytes rendered as lowercase hex, with no algorithm part; the model version lives in the bytes, not the address.
- **Legacy default:** a name without `@major` is read as `@1`, so pre-versioning objects stay decodable.
- `parseType`/`ResolveAny` split on `@`: `<type>` + `<major>`.

## 3. Semver for object models

| Bump | Change | Type name | Old data |
|---|---|---|---|
| MAJOR | incompatible serialization (removed/renamed fields, changed meaning) | `type@2` (new) | readable via the registered `type@1` deserializer |
| MINOR | additive fields with defaults | same `type@1` | still decodable by the new reader |
| PATCH | behavior fix, no format change | same `type@1` | unchanged |

- Within one MAJOR, old data MUST stay decodable by the new reader (MINOR/PATCH compatibility mirrors library-design §5). New fields are optional with sane zero-value defaults.
- Across a MAJOR, the app either registers the old-major deserializer alongside the new one, or migrates data (§5). The store keeps both versions coexisting — it never rewrites or drops old objects on its own.
- Unknown type/major on read → `ErrUnknownType` (graceful, detectable).

## 4. Registry & resolution

Registry is keyed by the **full versioned name**, so majors coexist: `RegisterType("blob@1", …)` and `RegisterType("blob@2", …)` in one store.

- `ResolveAny(ctx, h)` reads the versioned name from the bytes and dispatches to the matching deserializer; a graph MAY mix object versions (`commit@2` → `tree@1`).
- The generic core only carries the name through — it never interprets versions (cas-core §4.7).

## 5. Migration (model upgrade)

- **Read v1 → write v2:** the app reads old-major objects (registered `@1` deserializer), transforms them, `Put`s the new-major objects; the new graph replaces the old roots.
- Safety mirrors `operations.md` §5: keep both versions until the new data is verified; old objects are reclaimed only by the app's reachability (consistency §4) — never by the store.
- Versioned names make migration **observable**: the envelope type is visible per object (`meta`, the viewer's detail page), so an app can group objects by `type@major`.

## 6. gitlike reference

`gitlike` versions its four types from the start (`blob@1`, `tree@1`, `commit@1`, `tag@1`; cas-core §4.12). A future incompatible change (e.g. `TreeEntry` semantics) becomes `tree@2` with a registered `tree@1` deserializer — the pattern for app models.

## 7. Checklist

- [x] Every `Object[T].Type()` returns `<type>@<major>`
- [x] Versioned name travels with the serialized bytes (envelope)
- [x] Deserializers registered per full versioned name; multiple majors coexist in one store
- [x] Within a MAJOR: old data decodes with the new reader (additive fields with defaults)
- [x] Across a MAJOR: old deserializer registered or data migrated; old objects never dropped by the store
- [x] Unknown type/major → `ErrUnknownType`
- [x] Object-model versions never conflated with library/HTTP/doc versions
