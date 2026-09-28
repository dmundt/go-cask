# pack — canonical chunk + manifest helper layer

Package `pack` — lightweight, app-level helpers over packed payloads: fixed-size chunk splitting
and a manifest for metadata, written with the codec the caller names. A small, reusable layer above
the core `cas` store — not a new hash, codec, or object model, and not a storage backend: for
append-only stored objects see [../backend/packfs](../backend/packfs/README.md).

The core stays hash-agnostic and codec-agnostic. `cas/backend/fs` is the filesystem backend,
`cas/backend/packfs` the storage backend with a private pack index format; `cas/pack` is the
optional helper apps and examples use, never backend internals.

## Policy

- The codec is always the caller's, and the package imports none: every read and write names the
  codec at the call site (`SaveWith`/`LoadWith`, `EncodeWith`/`DecodeWith`, or `New` for a typed
  `Store[T]`). Nothing here substitutes one for a nil codec — a nil codec is `pack.ErrNilCodec`,
  because the codec decides what is written on disk.
- Every manifest read or write takes a `context.Context` first and reports `context.Canceled`
  instead of touching the filesystem.
- A manifest write is atomic: a temp file in the target directory, fsynced, then renamed, so a
  crash mid-write leaves the previous manifest intact instead of a truncated file that no longer
  decodes.
- The layer serves streaming or staged payload workflows that partition and annotate data without
  changing the underlying content-addressed identity rules. It is not a replacement for the typed
  `Store[T]` abstraction.

## Helper vs backend

```go
// helper layer: split payloads and write a sidecar manifest with the caller's codec
parts := pack.Split(payload, 1024)
codec := jsoncodec.New[pack.Data]() // cas/codec/json
_ = pack.SaveWith(ctx, "./state/manifest.json", pack.Data{"kind": "artifact"}, codec)

// backend layer: persist digests in an append-only packfile backend
raw, _ := packfs.New("./store", packfs.WithEnabled())
_ = raw
```

## Chunking example

```go
payload := []byte("hello world")
parts := pack.Split(payload, 5)
rebuilt := pack.Join(parts)
fmt.Println(string(rebuilt))
// hello world
```

## Manifest example

```go
meta := pack.Data{"kind": "artifact", "owner": "team-a"}
codec := jsoncodec.New[pack.Data]()
if err := pack.SaveWith(ctx, "./state/meta.json", meta, codec); err != nil {
	panic(err)
}
loaded, err := pack.LoadWith(ctx, "./state/meta.json", codec)
if err != nil {
	panic(err)
}
fmt.Println(loaded["kind"], loaded["owner"])
// artifact team-a
```
