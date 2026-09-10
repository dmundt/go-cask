---
type: Specification
title: Object Versioning — go-cask
description: Semantic versioning for object models — versioned type names carried in the self-describing TLV envelope, resolution without a registry, compatibility rules, and migration; the 4th, independent version space of go-cask.
version: v6
---

# Object Versioning — go-cask

Object models evolve; this is their **semantic versioning** contract: a versioned type name, coexistence of model versions in one store, compatibility rules — so a model change never makes old data unreadable. Related: `cas-core.md` §4.7 (`Object[T]`), §4.12 (gitlike), §8 decision 1 (the TLV envelope); `library-design.md` §2 (errors); `versioning.md` §6 (other version spaces); `consistency.md` (migration safety).

## 1. The version space

Object-model versions are a **fourth, independent version space** — separate from library Git tags, HTTP API majors, and doc revisions (versioning §6): scheme `type@major` in `Type()`, independent of library semver/HTTP API/docs. An app MAY bump its object model without a library release; a library release MAY add object types without an object-model bump.

## 2. Versioned type names

`Object[T].Type()` returns `<type>@<major>` (gitlike: `blob@1`, `tree@1`, `commit@1`, `tag@1`).

- The major is part of the type identity: the versioned name is written into the TLV envelope's Type field (`cas/envelope.go`; cas-core §8 decision 1), required regardless of codec.
- The address is unaffected — it is the raw digest bytes rendered as lowercase hex, with no algorithm part; the model version lives in the bytes, not the address.
- **Legacy default:** a name without `@major` is read as `@1`, so pre-versioning objects stay decodable.
- `parseType`/`ResolveAny` read the name back out of the envelope: `parseEnvelope` appends `@1` to a name that carries no major, and the resolver takes the `<type>` part before `@`.

## 3. Semver for object models

| Bump | Change | Type name | Old data |
|---|---|---|---|
| MAJOR | incompatible serialization (removed/renamed fields, changed meaning) | `type@2` (new) | readable while the app still handles `type@1` |
| MINOR | additive fields with defaults | same `type@1` | still decodable by the new reader |
| PATCH | behavior fix, no format change | same `type@1` | unchanged |

- Within one MAJOR, old data MUST stay decodable by the new reader (MINOR/PATCH compatibility mirrors library-design §5). New fields are optional with sane zero-value defaults.
- Across a MAJOR, the app either keeps handling the old major alongside the new one in its own resolver, or migrates data (§5). The store keeps both versions coexisting — it never rewrites or drops old objects on its own.
- Unknown type/major on read → `ErrUnknownType` (graceful, detectable).

## 4. Resolution — the envelope, not a registry

There is **no `RegisterType`, no registry, and no mutable global** anywhere in the code: the core is registry-free by design (cas-core §4.2, §4.7). A stored object is self-describing — `Store.Put` wraps the codec payload in the TLV envelope `[version u8][uvarint typeLen][type][uvarint payloadLen][payload]`, whose `type` field is the versioned `<type>@<major>` name (`cas/envelope.go`, cas-core §8 decision 1).

- `cas.EnvelopeFromBytes(data)` returns `Envelope{Type, Data}` (`Type` is the versioned name, `Data` an independent copy of the payload); the internal `parseEnvelope` appends `@1` when the stored name carries no major, so pre-versioning objects read as `@1`.
- An app's resolver reads that name and dispatches in its **own type switch**: `gitlike.parseType` wraps `cas.EnvelopeFromBytes` and cuts `env.Type` at `@` to get the base name, and `Resolver.ResolveAny` switches on it (`blob`/`tree`/`commit`/`tag` → the matching typed `Resolve*`); an unrecognized name is `ErrUnknownType`. No lookup table is consulted and nothing is registered at startup.
- Majors coexist because they are distinct stored names, not because they are registered: `blob@1` and `blob@2` are two different `Envelope.Type` values in one store. An app resolver that handles both MAY therefore mix them in one graph (`commit@2` → `tree@1`); one that handles a single major (like `gitlike` today, §6) reports the other as `ErrUnknownType`.
- The generic core only carries the name through — it never interprets versions (cas-core §4.7).
- Because the name is written into the stored bytes, the major is visible per object without side metadata (`meta`, the viewer's detail page), so an app can group objects by `type@major`.

## 5. Migration (model upgrade)

- **Read v1 → write v2:** the app reads old-major objects (its own `@1` handling), transforms them, `Put`s the new-major objects; the new graph replaces the old roots.
- Safety mirrors `operations.md` §5: keep both versions until the new data is verified; old objects are reclaimed only by the app's reachability (consistency §4) — never by the store.
- Versioned names make migration **observable**: the envelope type is visible per object (`meta`, the viewer's detail page), so an app can group objects by `type@major`.

## 6. gitlike reference

`gitlike` versions its four types from the start (`blob@1`, `tree@1`, `commit@1`, `tag@1`; cas-core §4.12). A future incompatible change (e.g. `TreeEntry` semantics) becomes `tree@2` that the app's own resolver handles alongside `tree@1` — the pattern for app models. `gitlike` today resolves only the base names (`blob`/`tree`/`commit`/`tag`) and takes no major into account, so it is deliberately a one-major reference; an app that needs two live majors extends its own type switch.

## 7. Checklist

- [x] Every `Object[T].Type()` returns `<type>@<major>`
- [x] Versioned name travels with the serialized bytes (envelope); no registry, no `RegisterType`
- [x] Resolution reads the name from the envelope and dispatches in the app's own type switch; multiple majors coexist in one store
- [x] Within a MAJOR: old data decodes with the new reader (additive fields with defaults)
- [x] Across a MAJOR: the app keeps handling the old major or migrates the data; old objects never dropped by the store
- [x] Unknown type/major → `ErrUnknownType`
- [x] Object-model versions never conflated with library/HTTP/doc versions
