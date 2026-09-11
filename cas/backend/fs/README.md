# fs — filesystem backend

Package `fs` provides the durable filesystem backend for the generic `cas` core.

It stores object bytes under a digest-addressed path, with configurable fan-out directories and atomic temp-file writes. This is the default backend for persistent use.

## Policy

- The backend stores raw bytes by digest; it knows nothing about app object types.
- The caller chooses the hash algorithm through `cas.Hasher`.
- The caller chooses the encoding through `Codec[T]`.
- `fs` is for durable storage; it is not a typed object model.

## Typical use

```go
raw, err := fs.New("./store")
```

Use `WithFanOut` and `WithFanLevels` to tune the directory layout when the store grows large.
