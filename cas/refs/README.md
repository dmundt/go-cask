# refs — mutable named pointers over a content-addressable store

Package `refs` implements the mutable half of a content-addressable store: named pointers ("refs") to a `cas.Digest`, each with an atomically-written current value and an append-only reflog. The immutable object graph already lives in `cas.Store[T]`; `refs` is how an application spells "which revision is current" without re-implementing atomic pointer writes, name validation, and prefix resolution on top of it — a gap `examples/files` and go-context's `internal/artifacts/refs.go` each closed independently before this package existed (go-cask#135).

## Policy

- `refs` is layered above the core: it stores plain `cas.Digest` values, never object bytes, and never reaches into a `cas.Backend`'s base directory.
- `Open` takes a plain filesystem directory the caller dedicates to refs (conventionally `filepath.Join(storeDir, "refs")`, beside a store's `objects/` tree) — the two trees are independent.
- Every `Set` is atomic (temp file + fsync + rename, plus a best-effort parent-directory fsync on POSIX) and appends one reflog entry, so `Log`/`Previous` never scan the object store the way a peek-every-object approach does.
- `Roots` is the ready-made root set for `cas.Reachable` before calling `Backend.GC`/`Backend.Prune` (see [docs/specs/consistency.md](../../docs/specs/consistency.md)). It returns each ref's **current** value only: the reflog is history, not a root source, so a sweep built from `Roots` reclaims every object that only `Log`/`Previous` still names. Feed those digests into the root set too when a recovery window is wanted.

## Example

```go
s, err := refs.Open(filepath.Join(storeDir, "refs"))
if err != nil {
	panic(err)
}

v1 := sha256.Of([]byte("release 1"))
if err := s.Set(ctx, "release/v1", v1); err != nil {
	panic(err)
}

current, err := s.Get(ctx, "release/v1")
if err != nil {
	panic(err)
}
fmt.Println(current.Equal(v1))
// true
```

## Resolve and Roots

```go
// Resolve accepts an exact name or an unambiguous prefix.
ref, err := s.Resolve(ctx, "release/v1")
if err != nil {
	panic(err)
}
fmt.Println(ref.Name)

// Roots feeds cas.Reachable before a GC/Prune pass.
roots, err := s.Roots(ctx)
if err != nil {
	panic(err)
}
reachable, err := cas.Reachable(ctx, refLister, roots)
if err != nil {
	panic(err)
}
```

## Reflog

`Log` returns a name's history newest first, capped by `limit` (0 or negative returns all entries); `Previous` returns the value a name held immediately before its most recent `Set`. A `Delete` appends a tombstone entry rather than erasing the log, mirroring how Git keeps a branch's reflog after the branch itself is deleted.

The log is history, not a root source: a sweep that uses `Roots` alone collects a digest that only the log still names, and a later `Get` on it is `ErrNotFound` even though `Previous` just returned it (consistency.md §4). Re-pinning an older revision from the log therefore requires the object to still exist — root the log's digests, or re-`Put` the content, before sweeping.

## Name validation

`ValidateName` rejects any name that is unsafe as a portable, cross-platform path component: empty names, absolute paths, `.`/`..` segments, a segment starting with `.` (also reserving the internal `.log` reflog directory), a segment ending in `.lock`, a segment ending in a space or `.` (Win32 silently trims these, so `"a "` and `"a"` would otherwise collide), a segment containing a Win32-reserved character (`<>:"|?*`) or backslash, a Win32-reserved device name (`CON`, `PRN`, `AUX`, `NUL`, `COM0`-`COM9`, `LPT0`-`LPT9`, with or without an extension), or a control character. A fuzz test (`FuzzValidateName`) exercises every accepted name through a real `Set`/`Get` round-trip to keep this rule set honest.

Use this package when an application needs a mutable, named pointer to content-addressed data — branches, HEAD-like markers, "latest" tags — without hand-rolling atomic writes or history tracking on top of `cas.Store[T]`.
