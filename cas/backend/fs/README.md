# fs — filesystem backend

Package `fs` — durable filesystem backend for the generic `cas` core. Stores object bytes under a
digest-addressed path with configurable fan-out directories and atomic temp-file writes; the
default backend for persistent use.

## Policy

- The base directory belongs to exactly one store: `List`/`Stats` report every digest-named file
  beneath it at any depth and `Clean` reclaims every `*.tmp` beneath it, so never point a backend
  at a directory that holds another store or app scratch `*.tmp` files. `fs.New` validates the
  base with `ValidateBase` before creating it, rejecting an empty path, `.`, a filesystem or
  volume root, and a parent-traversal path; a nested directory is accepted, because only the
  caller can say whether it already belongs to another store.
- `ValidateBase`, `EnsureBase`, `CleanupTemp` and `CleanTemp` are the exported pre-flight for a
  caller that owns the base path before a backend exists: check it, create it without opening a
  backend, reclaim its `*.tmp` crash leftovers, or sweep another tree by the same
  `*.tmp`/`*.tmp.<n>` convention with an age threshold and a removed count (the sweep
  `packfs.Clean` reuses for its pack directory). A caller that only opens a store needs none of
  them — `fs.New` validates, and `Backend.Clean` sweeps.

```go
backend, err := fs.New("./store")
```

Use `WithFanOut` and `WithFanLevels` to tune the directory layout when the store grows large.
