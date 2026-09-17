# pack — canonical chunk + manifest helper layer

Package `pack` groups the lightweight, app-level helpers that operate on packed payloads: fixed-size chunk splitting and a JSON sidecar manifest for metadata. It is intentionally a small, reusable layer above the core `cas` store rather than a new hash, codec, or object model.

## Policy

- The core `cas` package remains hash-agnostic and codec-agnostic.
- `pack` is the canonical helper layer for chunking and manifest metadata, not a replacement for the typed `Store[T]` abstraction.
- The layer is designed for streaming or staged payload workflows where data is partitioned and annotated without changing the underlying content-addressed identity rules.
- This is a helper package, not a storage backend. For append-only stored objects, see [../backend/pack](../backend/pack/README.md).

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
if err := pack.Save("./state/meta.json", meta); err != nil {
	panic(err)
}
loaded, err := pack.Load("./state/meta.json")
if err != nil {
	panic(err)
}
fmt.Println(loaded["kind"], loaded["owner"])
// artifact team-a
```

Use this package when a workflow needs both payload segmentation and a small metadata sidecar while keeping the content-addressed core unchanged.
