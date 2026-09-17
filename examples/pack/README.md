# Pack example

This example shows the canonical helper layer for content chunking and sidecar metadata.

The `cas/pack` package is intentionally small and reusable:

- `pack.Split` breaks a payload into fixed-size chunks
- `pack.Join` rebuilds the original bytes in order
- `pack.SaveWith` and `pack.LoadWith` let callers persist typed data with their own codec

## What this example demonstrates

1. Fixed-size chunking with a safe round trip
2. A typed `Chunk` + `Manifest` model that makes the data layout easy to read
3. JSON-backed persistence that stays outside the core codec stack

## Run it

```bash
go run ./examples/pack split 8 "hello world"
go run ./examples/pack roundtrip 8 "hello world"
go run ./examples/pack save /tmp/demo-pack.json artifact alice "hello world"
go run ./examples/pack load /tmp/demo-pack.json
```

The output should show chunk counts and the manifest values that were saved and loaded back.

## Typical flow

```go
type Chunk struct {
    Index int    `json:"index"`
    Size  int    `json:"size"`
    Data  string `json:"data"`
}

type Manifest struct {
    Kind      string  `json:"kind"`
    Owner     string  `json:"owner"`
    Chunks    []Chunk `json:"chunks,omitempty"`
    TotalSize int     `json:"total_size,omitempty"`
}

payload := []byte("hello world")
chunks := pack.Split(payload, 8)
reassembled := pack.Join(chunks)

manifest := Manifest{
    Kind:      "artifact",
    Owner:     "alice",
    Chunks:    []Chunk{{Index: 0, Size: len(payload), Data: string(payload)}},
    TotalSize: len(payload),
}

if err := pack.SaveWith("/tmp/demo-pack.json", manifest, json.New[Manifest]()); err != nil {
    panic(err)
}
loaded, err := pack.LoadWith("/tmp/demo-pack.json", json.New[Manifest]())
if err != nil {
    panic(err)
}
```

## Package note

This example keeps the `cas` core generic and hash-agnostic. `pack` sits above it as a helper for layout and metadata workflows rather than as a new serialization codec.
