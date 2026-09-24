# pack — canonical chunk + manifest helper layer

Package `pack` groups the lightweight, app-level helpers that operate on packed payloads: fixed-size chunk splitting and a manifest for metadata, written with the codec the caller names. It is intentionally a small, reusable layer above the core `cas` store rather than a new hash, codec, or object model.

The project’s canonical distinction is: `cas/backend/fs` is the filesystem backend, `cas/backend/packfs` is the storage backend with a private pack index format, and `cas/pack` is the optional helper used by apps and examples, not by backend internals.

## Policy

- The core `cas` package remains hash-agnostic and codec-agnostic.
- The codec is always the caller's. Nothing here substitutes one for a nil codec — a nil codec is `pack.ErrNilCodec`, because the codec decides what is written on disk. The JSON convenience is explicit: `SaveJSON`/`LoadJSON`/`EncodeJSON`/`DecodeJSON`.
- Every manifest read or write takes a `context.Context` first and reports `context.Canceled` instead of touching the filesystem.
- A manifest write is atomic: a temp file in the target directory, fsynced, then renamed, so a crash mid-write leaves the previous manifest intact instead of a truncated file that no longer decodes.
- `pack` is the canonical helper layer for chunking and manifest metadata, not a replacement for the typed `Store[T]` abstraction.
- The layer is designed for streaming or staged payload workflows where data is partitioned and annotated without changing the underlying content-addressed identity rules.
- This is a helper package, not a storage backend. For append-only stored objects, see [../backend/packfs](../backend/packfs/README.md).

## Helper vs backend

```go
// helper layer: split payloads and write a sidecar manifest
parts := pack.Split(payload, 1024)
_ = pack.SaveJSON(ctx, "./state/manifest.json", pack.Data{"kind": "artifact"})

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
if err := pack.SaveJSON(ctx, "./state/meta.json", meta); err != nil {
	panic(err)
}
loaded, err := pack.LoadJSON[pack.Data](ctx, "./state/meta.json")
if err != nil {
	panic(err)
}
fmt.Println(loaded["kind"], loaded["owner"])
// artifact team-a
```

A caller that wants another format passes its own codec to `SaveWith`/`LoadWith` (or builds a `Store[T]` with `New`); a caller that already holds encoded bytes uses `EncodeWith`/`DecodeWith`.

Use this package when a workflow needs both payload segmentation and a small metadata sidecar while keeping the content-addressed core unchanged.
