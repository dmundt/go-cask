---
title: Object Versioning — go-cask
description: Semantic versioning for object models — versioned type names, registry and resolution of multiple model versions, compatibility rules, and migration; the 4th, independent version space of go-cask.
version: v1
---

# Object Versioning — go-cask

> Objects (`Object[T]` implementations — `gitlike`'s blob/tree/commit/tag and
> every app's own types) evolve. This document gives object models a
> **semantic versioning** contract: a versioned type name, coexistence of
> several model versions in one store, and clear compatibility rules — so
> changing an object model never makes old data unreadable.
>
> Related: `.github/instructions/cas-core.instructions.md` §4.7 (the
> `Object[T]` contract), §4.12 (gitlike example), `.github/instructions/
> library-design.instructions.md` §2 (sentinel errors),
> `.github/instructions/versioning.instructions.md` §6 (the other version
> spaces), `.github/instructions/consistency.instructions.md` (migration
> safety).

---

## 1. The Version Space

Object-model versions are a **fourth, independent version space** — separate
from library Git tags, HTTP API majors, and doc revisions (versioning §6):

| Versioned thing    | Scheme                     | Independent of            |
| ------------------ | -------------------------- | ------------------------- |
| Object model       | `type@major` in `Type()`   | library semver, HTTP API, docs |

An app can bump its object model without a library release, and a library
release can add object types without an object-model bump.

---

## 2. Versioned Type Names

`Object[T].Type()` returns a **versioned type name**: `<type>@<major>`.

```go
func (b *Blob) Type() string   { return "blob@1" }
func (t *Tree) Type() string   { return "tree@1" }
func (c *Commit) Type() string { return "commit@1" }
func (g *Tag) Type() string    { return "tag@1" }
```

- The **major version is part of the type identity**: it travels with every
  serialized object (in the envelope `{"type": "commit@1", ...}` or the
  header `type commit@1\n...` — whichever serialization format §8 chooses,
  the versioned name is required either way).
- The digest-part of the address (`algo:hexdigest`) is unaffected — the
  object model version lives in the bytes, not in the address.
- **Legacy default**: a type name without `@major` is read as `@1`, so
  pre-versioning objects remain decodable.
- `parseType` / `ResolveAny` split on `@`: `<type>` + `<major>`.

---

## 3. Semver for Object Models

| Bump   | Change                                                        | Type name        | Old data                       |
| ------ | ------------------------------------------------------------- | ---------------- | ------------------------------ |
| MAJOR  | incompatible serialization: removed/renamed fields, changed meaning | `type@2` (new) | readable via the registered `type@1` deserializer |
| MINOR  | additive fields with defaults (old readers ignore unknown fields, new readers fill defaults) | same `type@1` | still decodable by the new reader |
| PATCH  | behavior fix, no format change                                | same `type@1`    | unchanged                      |

Rules:

- **Within one MAJOR, old data MUST stay decodable by the new reader** —
  MINOR/PATCH compatibility is part of the model contract (mirrors
  library-design §5). New fields are optional with sane zero-value defaults.
- **Across a MAJOR**, the app either registers the old-major deserializer
  alongside the new one, or migrates data (§5). The store keeps both versions
  coexisting — it never rewrites or drops old objects on its own.
- Unknown type/major on read → `ErrUnknownType` (graceful, detectable).

---

## 4. Registry & Resolution (multiple versions coexist)

The type registry is keyed by the **full versioned name**:

```go
RegisterType("blob@1", deserializeBlobV1)
RegisterType("blob@2", deserializeBlobV2) // both coexist in one store
```

- `ResolveAny(ctx, h)` reads the versioned name from the bytes and dispatches
  to the matching deserializer; a graph may freely mix object versions
  (`commit@2` pointing at a `tree@1`).
- The generic core only carries the name through — it never interprets
  versions (same as it never interprets types; cas-core §4.7).

---

## 5. Migration (object-model upgrade)

- **Read v1 → write v2**: an app-side migration reads old-major objects
  (registered `@1` deserializer), transforms them, and `Put`s the new-major
  objects — the new graph replaces the old roots.
- Safety mirrors `operations.instructions.md` §5: keep both versions until
  the new data is verified; old objects are only reclaimed by the app's
  reachability (consistency §4) — never by the store.
- The versioned type name makes migrations **observable**: `Stats`/viewer can
  report how many objects per `type@major` exist.

---

## 6. gitlike Reference

The `gitlike` example versions its four types from the start: `blob@1`,
`tree@1`, `commit@1`, `tag@1` (cas-core §4.12). A future incompatible change
(e.g. `TreeEntry` semantics) becomes `tree@2` with a registered `tree@1`
deserializer — demonstrating the pattern for app models.

---

## 7. Checklist

- [ ] Every `Object[T].Type()` returns `<type>@<major>`
- [ ] The versioned name travels with the serialized bytes (envelope)
- [ ] Deserializers are registered per full versioned name; multiple majors
      coexist in one store
- [ ] Within a MAJOR: old data decodes with the new reader (additive fields
      with defaults)
- [ ] Across a MAJOR: old deserializer registered or data migrated; old
      objects never dropped by the store
- [ ] Unknown type/major → `ErrUnknownType` (graceful)
- [ ] Object-model versions never conflated with library/HTTP/doc versions
