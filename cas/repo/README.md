# repo — a typed, cross-type object registry, walk, and reachability

Package `repo` promotes gitlike's example-only `Codecs`/`Repository`/`Resolver`/`WalkGraph` pattern into a supported, general-purpose package: a `Registry` that resolves a `cas.Digest` to its typed object across however many distinct `Object[T]` types an application registers, plus `Walk` and `Reachable` — the two operations a consumer with a typed graph previously had to hand-roll on top of `cas.Walker[T]` (which only ever follows one type through one `Store[T]`). Filed and specified as go-cask#136.

## Policy

- `repo` never redefines the storage model: every operation is built on `Object[T].References()`, the existing "single source of truth for traversal, preloading, and GC reachability" (`cas/object.go`).
- A `Registry` maps a versioned type name (`Object.Type()`, e.g. `"blob@1"`) to a `Decoder`; `Register` and `RegisterStore` both fail at construction time when a type name collides, is empty, or the decoder/store is nil — never a nil-decoder panic at first use.
- `Resolve` returns an `*UnknownTypeError` (`Unwrap() == cas.ErrUnknownType`) for a type nothing was registered for, never a nil dereference.
- `Walk` and `Reachable` depend on the `Resolver` interface, not the concrete `*Registry`, so a test double can stand in for one.
- `Walk` treats two failure modes differently: an unregistered type is reported to `visit` as an `*UnknownObject` and does **not** abort the walk (so `verify` can name it); any other resolve failure — most commonly a dangling reference — aborts immediately and is returned wrapped, so it is a named error rather than something silently skipped.
- `Reachable` is the documented, correct way to build the root set `Backend.GC`/`Backend.Prune` require for a typed, multi-type object graph (see [docs/specs/consistency.md](../../docs/specs/consistency.md) §4) — passing only entry-point roots without first expanding through `Reachable` silently deletes anything those roots reference.

## Example

```go
registry := repo.NewRegistry(raw, hasher)
if err := repo.RegisterStore(registry, "blob@1", blobs); err != nil {
	panic(err)
}
if err := repo.RegisterStore(registry, "tree@1", trees); err != nil {
	panic(err)
}

// Walk every reachable object, whichever of the registered types it is.
err := repo.Walk(ctx, registry, []cas.Digest{root}, func(d cas.Digest, obj repo.Object) error {
	fmt.Println(d, obj.Type())
	return nil
})

// Reachable is the root set to pass into Backend.GC/Backend.Prune.
reachable, err := repo.Reachable(ctx, registry, []cas.Digest{root})
```

## Unknown types are reported, not fatal

A digest whose envelope names a type nothing registered a `Decoder` for does not abort `Walk`/`Reachable`: it is handed to `visit` as an `*UnknownObject` (its `References()` is always `nil`, since an unknown type's references cannot be read), and the walk continues with the rest of the queue. A genuinely missing reference — a digest that does not exist in the backend at all — is a different failure: it aborts the walk and is returned wrapped in `cas.ErrNotFound`, because silently treating a broken reference as "no further references" would be a data-loss risk for `Reachable`/GC.

```go
err := repo.Walk(ctx, registry, roots, func(d cas.Digest, obj repo.Object) error {
	if unknown, ok := obj.(*repo.UnknownObject); ok {
		log.Printf("verify: %s has unregistered type %q", unknown.Digest, unknown.TypeName)
		return nil // do not abort the walk
	}
	return process(obj)
})
```

## Typed access beyond Resolve

`RegisterStore` also records the concrete `*cas.Store[T]` it wraps; `Registry.Stores()` hands back a `map[string]any` snapshot for a caller that needs typed operations beyond `Resolve` (e.g. `Put`ting a new object of a known type):

```go
blobStore := registry.Stores()["blob@1"].(*cas.Store[*gitlike.Blob])
```

Use this package instead of gitlike's example `Repository`/`Resolver`/`WalkGraph` when the application has more (or different) object types than gitlike's fixed four, or simply wants a supported library dependency rather than a copied example.
