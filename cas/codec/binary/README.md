# binary — compact custom payload codec

Package `binary` provides a generic `Codec[T]` for compact, caller-defined binary payloads in the
`cas` core. Intentionally object-agnostic: it encodes no `Blob`, `Tree`, or other app object model.
The caller supplies the exact `encode` and `decode` functions for the value type and the package
applies them as a standard `Codec[T]`.

The package follows the repo's single codec-stack model: a wrapper codec keeps an optional inner
codec and transforms the serialized bytes without changing the `cas` object model.

```go
codec := binary.New(
    json.New[MyType](),
    func(data []byte) ([]byte, error) { /* binary transport transform */ },
    func(data []byte) ([]byte, error) { /* reverse transform */ },
)
store := cas.New(raw, codec, sha256.New())
```

Direct custom binary payload without an inner codec, via `binary.NewRaw`:

```go
codec := binary.NewRaw(
    func(v MyType) ([]byte, error) { /* custom binary layout */ },
    func(data []byte) (MyType, error) { /* parse it back */ },
)
store := cas.New(raw, codec, sha256.New())
```

Use it for explicit, versioned binary layouts the application chooses — compact payloads with a
stable per-type schema and versioning strategy, not a single universal binary format. The
object-specific schema stays in the app or gitlike layer.

## Object-graph examples

Designed for application-defined object graphs such as `Blob` and `Tree`.

```go
// Blob: version + mode + length-prefixed payload.
type Blob struct {
    Mode string
    Data []byte
}

// Tree: version + count + repeated entries (name, mode, child hash).
type TreeEntry struct {
    Name string
    Mode string
    Hash cas.Digest
}

type Tree struct {
    Entries []TreeEntry
}
```

The `cas` core stays agnostic; the application chooses the binary wire format per type and passes
the encode/decode functions to `binary.New[T]`.

```go
codec := binary.New[Blob](encodeBlob, decodeBlob)
store := cas.New(raw, codec, sha256.New())
```

A tight tree example:

```go
func encodeTree(t Tree) ([]byte, error) {
    var b bytes.Buffer
    b.WriteByte(1) // version
    binary.Write(&b, binary.BigEndian, uint32(len(t.Entries)))
    for _, e := range t.Entries {
        binary.Write(&b, binary.BigEndian, uint32(len(e.Name)))
        b.WriteString(e.Name)
        binary.Write(&b, binary.BigEndian, uint32(len(e.Mode)))
        b.WriteString(e.Mode)
        b.Write(e.Hash[:])
    }
    return b.Bytes(), nil
}

func decodeTree(data []byte) (Tree, error) {
    if len(data) == 0 || data[0] != 1 {
        return Tree{}, fmt.Errorf("tree: bad version")
    }
    r := bytes.NewReader(data[1:])
    var n uint32
    binary.Read(r, binary.BigEndian, &n)
    out := make([]TreeEntry, 0, n)
    for i := uint32(0); i < n; i++ {
        var nameLen, modeLen uint32
        binary.Read(r, binary.BigEndian, &nameLen)
        name := make([]byte, nameLen)
        r.Read(name)
        binary.Read(r, binary.BigEndian, &modeLen)
        mode := make([]byte, modeLen)
        r.Read(mode)
        var h cas.Digest
        r.Read(h[:])
        out = append(out, TreeEntry{Name: string(name), Mode: string(mode), Hash: h})
    }
    return Tree{Entries: out}, nil
}
```
