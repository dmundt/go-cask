# Filesystem backend recipe

The filesystem backend is the simplest durable backend for local development and production use.

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
