# Filesystem backend recipe

The filesystem backend is the simplest durable backend for local development and production use.

## When to use it

- you want a local, persistent object store
- you are building a developer tool or CLI that needs durable artifacts
- you want the simplest backend to reason about before adding custom storage

## Example

```go
raw, err := fs.New("./repo")
if err != nil {
    panic(err)
}
```

## Good fit

- local content-addressable stores
- developer tooling
- object graphs and immutable content handling
- examples that need a persistent layout

## Important constraint

The backend remains a raw storage layer; it does not own application semantics or object invariants. Those stay in the typed layer above it.
